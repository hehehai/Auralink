import Foundation

// The steps of textio.paste, as pure logic: what is written to the
// clipboard, when ⌘V and the keys after it are posted, and when the old
// clipboard comes back. The platform parts (NSPasteboard, CGEvent,
// Accessibility, the front app) sit behind PasteEnvironment; the helper
// implements it for real, and the tests run the same steps against a fake
// with a virtual clock (a target app that reads the clipboard late).

/// Every item of the clipboard, each as its types' data (opaque here).
public typealias ClipboardItems = [[String: Data]]

/// The focused element of an app, as Accessibility reads it: its value
/// and the caret. Identity tells whether it is still the same element.
public struct FocusedValue: Sendable, Equatable {
    public var element: Int
    public var value: String
    public var caret: Int // UTF-16 offset
    public var selectionLength: Int

    public init(element: Int, value: String, caret: Int, selectionLength: Int) {
        self.element = element
        self.value = value
        self.caret = caret
        self.selectionLength = selectionLength
    }
}

public protocol PasteEnvironment: AnyObject {
    func nowMs() -> Double
    func sleep(ms: Double) async
    /// The app in front now.
    func frontApp() async -> App
    /// The clipboard's items, for putting them back later.
    func clipboardItems() async -> ClipboardItems
    /// Replaces the clipboard with text (marked transient for clipboard
    /// managers); returns the change count after the write.
    func writeClipboard(_ text: String) async -> Int
    func changeCount() async -> Int
    /// Puts items back, unless the clipboard was written since changeCount
    /// `expected`. True when they were put back.
    func restoreClipboard(_ items: ClipboardItems, unlessChangedFrom expected: Int) async -> Bool
    func postCommandV()
    func postKey(_ key: KeyPress)
    /// The focused element of pid, when Accessibility can read it.
    func focusedValue(pid: Int) async -> FocusedValue?
}

/// Whether the paste reached the focused field, as Accessibility shows it:
/// yes (its value grew by exactly the text), no (the value did not change
/// within the window), unknown (no value could be read, or it changed in
/// another way). "posted" only says ⌘V was sent.
public enum Landed: String, Sendable {
    case yes, no, unknown
}

public enum PasteRunner {
    /// How long the target gets to read the clipboard after ⌘V (the window
    /// of the confirmation, and the restore's wait when nothing can be
    /// read). Defaults are policy in Go; these are the helper's fallbacks.
    public static let defaultRestoreDelayMs = 1500
    public static let defaultPressDelayMs = 120
    /// How often the focused value is read while waiting for the paste.
    static let pollMs = 20.0

    public static func run(_ p: PasteParams, env: PasteEnvironment) async -> PasteResult {
        let t0 = env.nowMs()
        func ms() -> Double { roundTenth(env.nowMs() - t0) }
        let target = p.targetPid ?? 0
        if target != 0 {
            let now = await env.frontApp()
            if now.pid != target {
                return PasteResult(posted: false, restored: false, clipboardTaken: false, ms: ms(), front: now)
            }
        }
        let restore = !(p.keepClipboard ?? false)
        let window = Double(max(0, p.restoreDelayMs ?? defaultRestoreDelayMs))
        let snap = restore ? await env.clipboardItems() : []
        // Read before the write, so that the value can be compared after the paste.
        let before = target != 0 ? await env.focusedValue(pid: target) : nil
        let ours = await env.writeClipboard(p.text)
        // The write takes a moment: the front may have changed meanwhile.
        if target != 0 {
            let now = await env.frontApp()
            if now.pid != target {
                let back = restore ? await env.restoreClipboard(snap, unlessChangedFrom: ours) : false
                return PasteResult(posted: false, restored: back, clipboardTaken: restore && !back, ms: ms(), front: now)
            }
        }
        let vAt = env.nowMs()
        env.postCommandV()
        var landed = Landed.unknown
        if let before {
            landed = await confirm(before, text: p.text, pid: target, env: env, since: vAt, window: window)
        } else if restore {
            await env.sleep(ms: max(0, window - (env.nowMs() - vAt)))
        }
        // The keys after the paste: not when the text did not land.
        if let keys = p.press, !keys.isEmpty, landed != .no {
            let pressAt = vAt + Double(max(0, p.pressDelayMs ?? defaultPressDelayMs))
            await env.sleep(ms: max(0, pressAt - env.nowMs()))
            for k in keys { env.postKey(k) }
        }
        // Not confirmed: the field may still read the clipboard (a busy app
        // reads it late), so the text stays there and the old clipboard is
        // not put back. The app says so on its card; the user pastes by hand.
        var restored = false
        var taken = false
        var left = false
        if restore && landed != .no {
            restored = await env.restoreClipboard(snap, unlessChangedFrom: ours)
            taken = !restored
        } else if landed == .no {
            // Our text is still there unless someone wrote to the clipboard.
            let now = await env.changeCount()
            left = now == ours
        }
        return PasteResult(posted: true, restored: restored, clipboardTaken: taken, ms: ms(),
                           confirmed: landed.rawValue, confirmedMs: landed == .yes ? roundTenth(env.nowMs() - vAt) : nil,
                           leftOnClipboard: left ? true : nil)
    }

    /// Waits for the value of the same element to grow by the text (the
    /// selection it replaces is gone). Returns as soon as it does.
    private static func confirm(_ before: FocusedValue, text: String, pid: Int, env: PasteEnvironment,
                                since: Double, window: Double) async -> Landed {
        let expected = text.utf16.count - before.selectionLength
        while env.nowMs() - since < window {
            await env.sleep(ms: pollMs)
            guard let after = await env.focusedValue(pid: pid), after.element == before.element else { return .unknown }
            if after.value == before.value { continue }
            return after.value.utf16.count - before.value.utf16.count == expected ? .yes : .unknown
        }
        return .no
    }
}

private func roundTenth(_ x: Double) -> Double { (x * 10).rounded() / 10 }
