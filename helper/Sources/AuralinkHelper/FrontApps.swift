import AppKit
import ApplicationServices
import AuralinkHelperKit
import Foundation

/// The app in front, kept from NSWorkspace's activation notifications, so
/// any thread reads it without a hop to the main thread (the hotkey tap's
/// context snapshot, textio): app.activated tells the app of each change.
///
/// As an app comes to the front, an Electron app gets AXManualAccessibility
/// (Chromium builds its accessibility tree only when asked, and only a
/// moment later: set at activation, the tree is there when a shortcut
/// reads the selection).
final class FrontApps: @unchecked Sendable {
    let out: Writer
    private let lock = NSLock()
    private var app: App?
    private var started = false
    private var electron: [String: Bool] = [:] // bundle path → is Electron
    private let queue = DispatchQueue(label: "auralink.front", qos: .userInitiated)

    init(out: Writer) { self.out = out }

    /// Starts watching (once; on the main thread's run loop).
    func start() {
        let first = lock.withLock { () -> Bool in
            defer { started = true }
            return !started
        }
        guard first else { return }
        DispatchQueue.main.async { [self] in
            if let a = NSWorkspace.shared.frontmostApplication {
                lock.withLock { app = Self.describe(a) }
            }
            NSWorkspace.shared.notificationCenter.addObserver(forName: NSWorkspace.didActivateApplicationNotification,
                                                              object: nil, queue: nil) { [weak self] n in
                guard let self, let a = n.userInfo?[NSWorkspace.applicationUserInfoKey] as? NSRunningApplication else { return }
                self.activated(a)
            }
        }
    }

    static func describe(_ a: NSRunningApplication) -> App {
        App(pid: Int(a.processIdentifier), bundleId: a.bundleIdentifier, name: a.localizedName)
    }

    private func activated(_ a: NSRunningApplication) {
        let desc = Self.describe(a)
        lock.withLock { app = desc }
        out.notify(Method.appActivated, desc)
        let bundle = a.bundleURL
        queue.async { [self] in
            if let bundle, AXIsProcessTrusted(), isElectron(bundle) {
                let r = AXUIElementSetAttributeValue(AXUIElementCreateApplication(a.processIdentifier),
                                                     "AXManualAccessibility" as CFString, kCFBooleanTrue)
                if r != .success { log("AXManualAccessibility on \(desc.bundleId ?? "?"): \(r.rawValue)") }
            }
        }
    }

    private func isElectron(_ bundle: URL) -> Bool {
        let key = bundle.path
        if let known = lock.withLock({ electron[key] }) { return known }
        let is_ = FileManager.default.fileExists(atPath: bundle.appendingPathComponent("Contents/Frameworks/Electron Framework.framework").path)
        lock.withLock { electron[key] = is_ }
        return is_
    }

    /// The app in front now: the cache, else NSWorkspace (on the main thread).
    func current() async -> App {
        if let a = cached { return a }
        return await onMain { NSWorkspace.shared.frontmostApplication.map(FrontApps.describe) ?? App(pid: 0, bundleId: nil, name: nil) }
    }

    /// The app in front as last reported, from any thread.
    var cached: App? { lock.withLock { app } }
}
