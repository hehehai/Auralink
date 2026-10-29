import Foundation
import XCTest
@testable import AuralinkHelperKit

/// textio.paste's steps against a fake pasteboard and a fake target app on
/// a virtual clock. Nothing here touches NSPasteboard, the keyboard or the
/// real Accessibility tree. The target reads the clipboard `latency` ms
/// after ⌘V (a browser's renderer may be busy that long) and inserts what
/// it reads at its caret.
final class PasteRunnerTests: XCTestCase {
    static let text = "会议纪要：下周二发布"
    static let utf8 = "public.utf8-plain-text"
    static let target = App(pid: 900, bundleId: "company.thebrowser.Browser", name: "Arc")
    static let other = App(pid: 530, bundleId: "com.apple.TextEdit", name: "TextEdit")
    static let original: ClipboardItems = [[utf8: Data("原来的剪贴板".utf8)]]

    final class FakePaste: PasteEnvironment, @unchecked Sendable {
        var now: Double = 0
        var events: [(at: Double, run: () -> Void)] = []
        var front: App
        var clipboard: ClipboardItems
        var changes = 0

        /// The target app: a read `latency` ms after ⌘V (nil: never reads),
        /// and whether Accessibility can read its focused element.
        var latency: Double?
        var axReadable: Bool
        var value = ""
        var caret = 0
        /// Whether the target is focused at all (the element of the paste).
        var focused = true

        /// Set: the front app changes when the clipboard is written (a
        /// user click between the write and ⌘V).
        var switchFrontOnWrite: App?

        var targetInserts: [String] = []
        var commandVs: [App] = []  // whom ⌘V reached
        var keys: [KeyPress] = []

        init(front: App, clipboard: ClipboardItems, latency: Double?, axReadable: Bool) {
            self.front = front
            self.clipboard = clipboard
            self.latency = latency
            self.axReadable = axReadable
        }

        func nowMs() -> Double { now }

        func sleep(ms: Double) async {
            now += ms
            fire()
        }

        func schedule(after ms: Double, _ run: @escaping () -> Void) {
            events.append((now + ms, run))
        }

        /// Runs the scheduled events that are due, earliest first.
        func fire() {
            while let i = events.indices.min(by: { events[$0].at < events[$1].at }), events[i].at <= now {
                let e = events.remove(at: i)
                e.run()
            }
        }

        func frontApp() async -> App { front }

        func clipboardItems() async -> ClipboardItems { clipboard }

        func clipboardText() -> String? {
            clipboard.first?[PasteRunnerTests.utf8].flatMap { String(data: $0, encoding: .utf8) }
        }

        func writeClipboard(_ text: String) async -> Int {
            clipboard = [[PasteRunnerTests.utf8: Data(text.utf8)]]
            changes += 1
            if let f = switchFrontOnWrite {
                front = f
                switchFrontOnWrite = nil
            }
            return changes
        }

        func changeCount() async -> Int { changes }

        func restoreClipboard(_ items: ClipboardItems, unlessChangedFrom expected: Int) async -> Bool {
            guard changes == expected else { return false }
            clipboard = items
            changes += 1
            return true
        }

        func postCommandV() {
            commandVs.append(front)
            guard front.pid == PasteRunnerTests.target.pid, focused, let latency else { return }
            // The target reads the clipboard now and inserts what it read.
            schedule(after: latency) { [self] in
                let s = clipboardText() ?? ""
                let units = Array(value.utf16)
                let ins = Array(s.utf16)
                value = String(decoding: units[..<caret] + ins + units[caret...], as: UTF16.self)
                caret += ins.count
                targetInserts.append(s)
            }
        }

        func postKey(_ key: KeyPress) {
            keys.append(key)
        }

        func focusedValue(pid: Int) async -> FocusedValue? {
            guard axReadable, pid == PasteRunnerTests.target.pid, focused else { return nil }
            return FocusedValue(element: 7, value: value, caret: caret, selectionLength: 0)
        }
    }

    /// Runs the paste, then lets the target act (its reads come later than
    /// the helper's answer when it is slow).
    private func run(_ p: PasteParams, _ env: FakePaste) async -> PasteResult {
        let res = await PasteRunner.run(p, env: env)
        await env.sleep(ms: 5000)
        return res
    }

    private func params(keep: Bool? = nil, restoreMs: Int? = nil, press: [KeyPress]? = nil) -> PasteParams {
        PasteParams(text: Self.text, keepClipboard: keep, restoreDelayMs: restoreMs, targetPid: Self.target.pid,
                    press: press, pressDelayMs: nil)
    }

    /// A fast target gets our text, and the old clipboard comes back.
    func testFastTargetGetsTheTextAndTheClipboardIsRestored() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        let res = await run(params(), env)
        XCTAssertTrue(res.posted)
        XCTAssertEqual(env.targetInserts, [Self.text])
        XCTAssertEqual(env.value, Self.text)
        XCTAssertEqual(env.clipboard, Self.original)
        XCTAssertTrue(res.restored)
    }

    /// Regression: a target that reads the clipboard after the fixed 500 ms
    /// restore pastes the OLD clipboard (or nothing) while the helper reports
    /// posted. Arc's renderer took >500 ms on busy pages (mac-air log: every
    /// Arc paste returned after ~530 ms).
    func testLateTargetWithReadableValueStillGetsTheText() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 900, axReadable: true)
        _ = await run(params(), env)
        XCTAssertEqual(env.targetInserts, [Self.text], "the target read the old clipboard: \(env.targetInserts)")
    }

    /// The same when Accessibility cannot read the field's value: the
    /// restore must wait long enough for a slow target by default.
    func testLateTargetWithoutAccessibilityValueStillGetsTheText() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 900, axReadable: false)
        _ = await run(params(), env)
        XCTAssertEqual(env.targetInserts, [Self.text], "the target read the old clipboard: \(env.targetInserts)")
    }

    /// A user copy during the wait is kept (the change count), and our
    /// text is not put back over it.
    func testUserCopyDuringTheWaitIsKept() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        let user: ClipboardItems = [[Self.utf8: Data("用户刚复制的".utf8)]]
        env.schedule(after: 10) { env.clipboard = user; env.changes += 1 }
        let res = await run(params(), env)
        XCTAssertFalse(res.restored)
        XCTAssertTrue(res.clipboardTaken)
        XCTAssertEqual(env.clipboard, user)
    }

    /// Regression: the front app changes after the clipboard is written and
    /// before ⌘V. The helper checked the front once, at the start: ⌘V went to
    /// the other app and the helper still said posted.
    func testFrontChangeBetweenTheWriteAndCommandVPostsNothing() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        env.switchFrontOnWrite = Self.other
        let res = await run(params(), env)
        XCTAssertFalse(res.posted, "⌘V went to \(env.commandVs)")
        XCTAssertTrue(env.commandVs.isEmpty)
        XCTAssertEqual(env.clipboard, Self.original, "the clipboard is put back when nothing was pasted")
    }

    func testFrontChangeBeforeThePasteIsRefused() async {
        let env = FakePaste(front: Self.other, clipboard: Self.original, latency: 40, axReadable: true)
        let res = await run(params(), env)
        XCTAssertFalse(res.posted)
        XCTAssertEqual(res.front?.pid, Self.other.pid)
        XCTAssertTrue(env.commandVs.isEmpty)
        XCTAssertEqual(env.clipboard, Self.original)
    }

    /// The target never reads (say, its field is not focused after all): the
    /// helper says so ("no"), presses nothing, and gives the clipboard back.
    func testNoLandingIsReportedAndNothingIsPressed() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: nil, axReadable: true)
        let res = await run(params(press: [KeyPress(key: 36, modifiers: nil)]), env)
        XCTAssertTrue(res.posted)
        XCTAssertEqual(res.confirmed, "no")
        XCTAssertTrue(env.keys.isEmpty, "a key was pressed after a paste that did not land")
        XCTAssertEqual(env.clipboard, Self.original)
    }

    /// A paste the field shows at once is confirmed, and the clipboard goes
    /// back at once (no fixed wait).
    func testConfirmedPasteRestoresAtOnce() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        let res = await run(params(), env)
        XCTAssertEqual(res.confirmed, "yes")
        XCTAssertEqual(res.confirmedMs, 40)
        XCTAssertLessThan(res.ms, 200)
    }

    /// Without a readable value the helper cannot confirm; it waits the
    /// whole window (1500 ms by default) before it gives the clipboard back.
    func testUnreadableValueIsUnknownAndWaitsTheWindow() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: false)
        let res = await run(params(), env)
        XCTAssertEqual(res.confirmed, "unknown")
        XCTAssertNil(res.confirmedMs)
        XCTAssertEqual(res.ms, 1500, accuracy: 0.5)
    }

    func testKeepClipboardLeavesTheText() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        let res = await run(params(keep: true), env)
        XCTAssertTrue(res.posted)
        XCTAssertEqual(env.clipboard, [[Self.utf8: Data(Self.text.utf8)]])
    }

    func testKeysAfterThePasteAreSent() async {
        let env = FakePaste(front: Self.target, clipboard: Self.original, latency: 40, axReadable: true)
        _ = await run(params(press: [KeyPress(key: 36, modifiers: nil)]), env)
        XCTAssertEqual(env.keys.map(\.key), [36])
        XCTAssertEqual(env.targetInserts, [Self.text])
    }
}
