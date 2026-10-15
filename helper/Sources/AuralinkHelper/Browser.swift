import AppKit
import ApplicationServices
import AuralinkHelperKit
import Foundation

// The URL of a browser's front tab, for app enhancement's URL rules,
// through Accessibility only: no AppleScript, so no Automation consent to
// ask per browser (the user's choice, 2026-10-15). In order:
//
//  1. the AXURL of the outermost AXWebArea around the focused element:
//     the tab the user types in (also in a split view; an iframe's web
//     area inside it is skipped);
//  2. the focused (else main) window's AXDocument: Chromium browsers and
//     Safari name the front tab's address there;
//  3. the AXURL of the window's first AXWebArea, breadth first (Arc).
//
// Every browser goes the same way: Safari, the Chromium family (Chrome,
// Edge, Brave, Arc, Vivaldi, Opera), Firefox, and the browsers the user
// adds. The calls run on a queue of their own (Accessibility is
// thread-safe), each element answering within the messaging timeout.

enum Browser {
    private static let queue = DispatchQueue(label: "auralink.browser", qos: .userInitiated, attributes: .concurrent)

    /// Nodes the breadth-first search visits at most.
    static let maxNodes = 400

    static func url(_ p: BrowserURLParams, front: FrontApps) async -> BrowserURLResult {
        let t0 = monotonicMs()
        var app: NSRunningApplication?
        if let pid = p.pid, pid > 0 {
            app = NSRunningApplication(processIdentifier: pid_t(pid))
        } else if let id = p.bundleId, !id.isEmpty {
            app = NSRunningApplication.runningApplications(withBundleIdentifier: id).first { !$0.isTerminated }
        } else {
            let f = await front.current()
            if f.pid > 0 { app = NSRunningApplication(processIdentifier: pid_t(f.pid)) }
        }
        let bundleID = app?.bundleIdentifier ?? p.bundleId ?? ""
        guard let app, !app.isTerminated else {
            return BrowserURLResult(bundleId: bundleID, ms: round1(monotonicMs() - t0),
                                    error: "\(bundleID.isEmpty ? "the app" : bundleID) is not running")
        }
        guard AXIsProcessTrusted() else {
            return BrowserURLResult(bundleId: bundleID, ms: round1(monotonicMs() - t0), error: "Accessibility is not allowed")
        }
        let pid = app.processIdentifier
        let (url, via, err) = await withCheckedContinuation { k in
            queue.async { k.resume(returning: read(pid: pid)) }
        }
        return BrowserURLResult(bundleId: bundleID, url: url, via: via, ms: round1(monotonicMs() - t0), error: url == nil ? err : nil)
    }

    private static func attribute(_ e: AXUIElement, _ name: String) -> CFTypeRef? {
        var v: CFTypeRef?
        guard AXUIElementCopyAttributeValue(e, name as CFString, &v) == .success else { return nil }
        return v
    }

    private static func element(_ v: CFTypeRef?) -> AXUIElement? {
        guard let v, CFGetTypeID(v) == AXUIElementGetTypeID() else { return nil }
        return (v as! AXUIElement)
    }

    private static func role(_ e: AXUIElement) -> String { attribute(e, kAXRoleAttribute) as? String ?? "" }

    /// A web area's address.
    private static func webURL(_ e: AXUIElement) -> String? {
        let v = attribute(e, kAXURLAttribute)
        if let u = v as? URL { return u.absoluteString }
        if let s = v as? String, !s.isEmpty { return s }
        return nil
    }

    private static func read(pid: pid_t) -> (String?, String, String) {
        let app = AXUIElementCreateApplication(pid)
        AXUIElementSetMessagingTimeout(app, 0.3)
        // 1. Around the focus.
        if let focused = element(attribute(app, kAXFocusedUIElementAttribute)) {
            var chain: [AXUIElement] = []
            var roles: [String] = []
            var e: AXUIElement? = focused
            while let cur = e, chain.count < 60 {
                let r = role(cur)
                chain.append(cur)
                roles.append(r)
                if r == kAXWindowRole as String || r == kAXApplicationRole as String { break }
                e = element(attribute(cur, kAXParentAttribute))
            }
            if let i = outermost(roles, role: "AXWebArea"), let url = webURL(chain[i]) {
                return (url, URLVia.axFocus, "")
            }
        }
        // 2. The window's document.
        guard let win = element(attribute(app, kAXFocusedWindowAttribute)) ?? element(attribute(app, kAXMainWindowAttribute)) else {
            return (nil, URLVia.none, "no focused window")
        }
        if let s = attribute(win, kAXDocumentAttribute) as? String, !s.isEmpty {
            return (s, URLVia.axDocument, "")
        }
        // 3. The first web area, breadth first.
        var queue: [AXUIElement] = [win]
        var seen = 0
        while !queue.isEmpty, seen < maxNodes {
            let e = queue.removeFirst()
            seen += 1
            if role(e) == "AXWebArea" {
                if let url = webURL(e) { return (url, URLVia.axWebURL, "") }
                continue
            }
            if let kids = attribute(e, kAXChildrenAttribute) as? [AXUIElement] {
                queue.append(contentsOf: kids)
            }
        }
        return (nil, URLVia.none, "no AXDocument or web area URL (visited \(seen))")
    }
}

/// The apps to pick from (app.list): the regular running apps, and with
/// installed the bundles in the Applications folders (one level of
/// folders down: /Applications/Utilities, a vendor's folder).
enum AppList {
    static func list(installed: Bool) async -> AppListResult {
        let running = await onMain {
            NSWorkspace.shared.runningApplications.compactMap { a -> AppInfo? in
                guard a.activationPolicy == .regular, let id = a.bundleIdentifier, !a.isTerminated else { return nil }
                return AppInfo(bundleId: id, name: a.localizedName ?? id, running: true, pid: Int(a.processIdentifier), path: a.bundleURL?.path)
            }
        }
        var found: [AppInfo] = []
        if installed {
            let fm = FileManager.default
            let roots = ["/Applications", "/System/Applications", "/System/Volumes/Preboot/Cryptexes/App/System/Applications",
                         NSHomeDirectory() + "/Applications"]
            func add(_ url: URL) {
                guard let b = Bundle(url: url), let id = b.bundleIdentifier else { return }
                let name = fm.displayName(atPath: url.path).replacingOccurrences(of: ".app", with: "")
                found.append(AppInfo(bundleId: id, name: name, running: false, path: url.path))
            }
            for root in roots {
                guard let items = try? fm.contentsOfDirectory(at: URL(fileURLWithPath: root), includingPropertiesForKeys: [.isDirectoryKey]) else { continue }
                for item in items {
                    if item.pathExtension == "app" {
                        add(item)
                    } else if (try? item.resourceValues(forKeys: [.isDirectoryKey]))?.isDirectory == true,
                              let inner = try? fm.contentsOfDirectory(at: item, includingPropertiesForKeys: nil) {
                        for app in inner where app.pathExtension == "app" { add(app) }
                    }
                }
            }
        }
        return AppListResult(apps: mergeApps(running: running, installed: found))
    }
}

func round1(_ v: Double) -> Double { (v * 10).rounded() / 10 }
