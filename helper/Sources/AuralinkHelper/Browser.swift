import AppKit
import ApplicationServices
import AuralinkHelperKit
import Carbon
import Foundation

// The URL of a browser's front tab, for app enhancement's URL rules.
// NSAppleScript when Auralink may script the browser (the user's
// Automation consent, asked once per browser by macOS), then
// Accessibility: the focused window's AXDocument, or the AXURL of its web
// area (no consent needed; Safari in the background has neither).
//
// AppleScript never runs without consent: on a browser not determined yet
// it would make macOS prompt out of the blue. The state is probed without
// prompting (AEDeterminePermissionToAutomateTarget, 9–235 ms), kept, and
// probed again as the browser comes to the front. Compiled scripts are
// kept too (compiling costs 50–500 ms); they run on the main thread.

@MainActor private var compiled: [String: NSAppleScript] = [:]

final class Browser: @unchecked Sendable {
    private let lock = NSLock()
    private var states: [String: Int] = [:] // bundle id → AEDeterminePermission status
    private let probeQueue = DispatchQueue(label: "auralink.automation", qos: .userInitiated, attributes: .concurrent)

    /// An app came to the front: refresh a known browser's state.
    func activated(_ app: App) {
        guard let id = app.bundleId, knownBrowser(id) != nil else { return }
        let code = Int(automationStatus(id, ask: false))
        lock.withLock { states[id] = code }
    }

    /// The Automation status of a browser, probed when not known.
    private func state(_ id: String) async -> Int {
        if let s = lock.withLock({ states[id] }), s != -600 { return s }
        let code = await probeCode(id, ask: false)
        lock.withLock { states[id] = code }
        return code
    }

    private func probeCode(_ id: String, ask: Bool) async -> Int {
        await withCheckedContinuation { k in
            probeQueue.async { k.resume(returning: Int(automationStatus(id, ask: ask))) }
        }
    }

    func url(_ bundleID: String) async -> BrowserURLResult {
        let t0 = monotonicMs()
        var r = BrowserURLResult(bundleId: bundleID)
        guard !bundleID.isEmpty,
              let app = NSRunningApplication.runningApplications(withBundleIdentifier: bundleID).first(where: { !$0.isTerminated }) else {
            r.error = "\(bundleID.isEmpty ? "no app" : bundleID) is not running"
            r.ms = monotonicMs() - t0
            return r
        }
        if let b = knownBrowser(bundleID) {
            let code = await state(bundleID)
            if code == 0 {
                let a0 = monotonicMs()
                let (url, err, msg) = await runScripts(b.scripts)
                r.appleScriptMs = round1(monotonicMs() - a0)
                if let url {
                    r.url = url
                    r.via = URLVia.appleScript
                } else {
                    r.error = "AppleScript \(err ?? 0): \(msg ?? "no URL")"
                    if let err, isFinalAppleScriptError(err) {
                        lock.withLock { states[bundleID] = err }
                        r.automation = AutomationStateName.of(err)
                    }
                }
            } else {
                r.automation = AutomationStateName.of(code)
            }
        }
        if r.url == nil {
            let a0 = monotonicMs()
            let (url, via, err) = axURL(pid: app.processIdentifier)
            r.axMs = round1(monotonicMs() - a0)
            if let url {
                r.url = url
                r.via = via
                r.error = nil
            } else if let err {
                r.error = err
            }
        }
        r.ms = round1(monotonicMs() - t0)
        return r
    }

    /// Runs the scripts in order on the main thread, compiled once.
    private func runScripts(_ sources: [String]) async -> (String?, Int?, String?) {
        await onMain {
            var lastErr: Int?
            var lastMsg: String?
            for source in sources {
                let script: NSAppleScript
                if let s = compiled[source] {
                    script = s
                } else {
                    guard let s = NSAppleScript(source: "with timeout of 1 seconds\n\(source)\nend timeout") else { continue }
                    var cerr: NSDictionary?
                    s.compileAndReturnError(&cerr)
                    compiled[source] = s
                    script = s
                }
                var err: NSDictionary?
                let out = script.executeAndReturnError(&err)
                if let s = out.stringValue, !s.isEmpty { return (s, nil, nil) }
                lastErr = err?[NSAppleScript.errorNumber] as? Int
                lastMsg = err?[NSAppleScript.errorMessage] as? String
                if isFinalAppleScriptError(lastErr) { break }
            }
            return (nil, lastErr, lastMsg)
        }
    }

    /// The focused window's AXDocument, else the AXURL of the first web area.
    private func axURL(pid: pid_t) -> (String?, String, String?) {
        guard AXIsProcessTrusted() else { return (nil, URLVia.none, "Accessibility is not allowed") }
        let app = AXUIElementCreateApplication(pid)
        AXUIElementSetMessagingTimeout(app, 0.3)
        var winRef: CFTypeRef?
        var st = AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute as CFString, &winRef)
        if st != .success {
            st = AXUIElementCopyAttributeValue(app, kAXMainWindowAttribute as CFString, &winRef)
        }
        guard st == .success, let winRef, CFGetTypeID(winRef) == AXUIElementGetTypeID() else {
            return (nil, URLVia.none, "no focused window (\(st.rawValue))")
        }
        let win = winRef as! AXUIElement
        var doc: CFTypeRef?
        if AXUIElementCopyAttributeValue(win, kAXDocumentAttribute as CFString, &doc) == .success, let s = doc as? String, !s.isEmpty {
            return (s, URLVia.axDocument, nil)
        }
        // Breadth-first for an AXWebArea with an AXURL.
        var queue: [AXUIElement] = [win]
        var seen = 0
        while !queue.isEmpty, seen < 400 {
            let e = queue.removeFirst()
            seen += 1
            var role: CFTypeRef?
            AXUIElementCopyAttributeValue(e, kAXRoleAttribute as CFString, &role)
            if (role as? String) == "AXWebArea" {
                var u: CFTypeRef?
                if AXUIElementCopyAttributeValue(e, kAXURLAttribute as CFString, &u) == .success {
                    if let url = u as? URL { return (url.absoluteString, URLVia.axWebURL, nil) }
                    if let s = u as? String { return (s, URLVia.axWebURL, nil) }
                }
            }
            var kids: CFTypeRef?
            if AXUIElementCopyAttributeValue(e, kAXChildrenAttribute as CFString, &kids) == .success, let arr = kids as? [AXUIElement] {
                queue.append(contentsOf: arr)
            }
        }
        return (nil, URLVia.none, "no AXDocument or web area URL (visited \(seen))")
    }

    /// Each browser's state, without prompting (in parallel).
    func probe(_ ids: [String]?) async -> AutomationProbeResult {
        let list = (ids?.isEmpty ?? true) ? knownBrowsers.map(\.bundleID) : ids!
        let states = await withTaskGroup(of: (Int, AutomationState).self) { group in
            for (i, id) in list.enumerated() {
                group.addTask { (i, await self.describe(id, ask: false)) }
            }
            var out: [(Int, AutomationState)] = []
            for await s in group { out.append(s) }
            return out.sorted { $0.0 < $1.0 }.map(\.1)
        }
        return AutomationProbeResult(browsers: states)
    }

    /// Asks macOS (it prompts once per browser) and waits for the answer.
    /// macOS answers only for a running browser: launch opens it first,
    /// in the background.
    func request(_ id: String, launch: Bool) async throws -> AutomationState {
        guard let url = NSWorkspace.shared.urlForApplication(withBundleIdentifier: id) else {
            throw RPCError(Code.unsupported, "\(id) is not installed", data: .object(["capability": .string(Method.automationRequest)]))
        }
        if launch, !isRunning(id) {
            let config = NSWorkspace.OpenConfiguration()
            config.activates = false
            config.hides = true
            _ = try? await NSWorkspace.shared.openApplication(at: url, configuration: config)
            // Apple Events reach it a moment after it is running.
            for _ in 0 ..< 50 where !isRunning(id) {
                try? await Task.sleep(nanoseconds: 100_000_000)
            }
            try? await Task.sleep(nanoseconds: 500_000_000)
        }
        return await describe(id, ask: true)
    }

    private func isRunning(_ id: String) -> Bool {
        NSRunningApplication.runningApplications(withBundleIdentifier: id).contains { !$0.isTerminated }
    }

    private func describe(_ id: String, ask: Bool) async -> AutomationState {
        let name = knownBrowser(id)?.name ?? NSWorkspace.shared.urlForApplication(withBundleIdentifier: id)
            .map { FileManager.default.displayName(atPath: $0.path).replacingOccurrences(of: ".app", with: "") } ?? id
        let installed = NSWorkspace.shared.urlForApplication(withBundleIdentifier: id) != nil
        guard installed else {
            return AutomationState(bundleId: id, name: name, installed: false, running: false, state: AutomationStateName.notInstalled, code: 0)
        }
        let code = await probeCode(id, ask: ask)
        lock.withLock { states[id] = code }
        return AutomationState(bundleId: id, name: name, installed: true, running: isRunning(id), state: AutomationStateName.of(code), code: code)
    }
}

func automationStatus(_ bundleID: String, ask: Bool) -> OSStatus {
    let target = NSAppleEventDescriptor(bundleIdentifier: bundleID)
    guard let desc = target.aeDesc else { return OSStatus(errAEEventNotPermitted) }
    return AEDeterminePermissionToAutomateTarget(desc, AEEventClass(typeWildCard), AEEventID(typeWildCard), ask)
}

func round1(_ v: Double) -> Double { (v * 10).rounded() / 10 }
