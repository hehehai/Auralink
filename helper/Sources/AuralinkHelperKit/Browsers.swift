import Foundation

/// A browser whose front tab the helper can ask for by AppleScript.
public struct KnownBrowser: Sendable, Equatable {
    public var bundleID: String
    public var name: String
    /// AppleScript sources returning the front tab's URL, tried in order.
    public var scripts: [String]
}

/// The browsers the helper scripts: Safari's dictionary, and Chromium's
/// for the Chromium family. Other browsers (and apps) are read through
/// Accessibility only.
public let knownBrowsers: [KnownBrowser] = {
    func chromium(_ id: String, _ name: String) -> KnownBrowser {
        KnownBrowser(bundleID: id, name: name, scripts: [
            "tell application id \"\(id)\" to return URL of active tab of front window",
        ])
    }
    return [
        KnownBrowser(bundleID: "com.apple.Safari", name: "Safari", scripts: [
            "tell application id \"com.apple.Safari\" to return URL of current tab of front window",
            "tell application id \"com.apple.Safari\" to return URL of front document",
        ]),
        KnownBrowser(bundleID: "com.apple.SafariTechnologyPreview", name: "Safari Technology Preview", scripts: [
            "tell application id \"com.apple.SafariTechnologyPreview\" to return URL of current tab of front window",
        ]),
        chromium("com.google.Chrome", "Google Chrome"),
        chromium("com.google.Chrome.beta", "Google Chrome Beta"),
        chromium("com.microsoft.edgemac", "Microsoft Edge"),
        chromium("com.brave.Browser", "Brave Browser"),
        chromium("company.thebrowser.Browser", "Arc"),
        chromium("com.vivaldi.Vivaldi", "Vivaldi"),
        chromium("com.operasoftware.Opera", "Opera"),
    ]
}()

/// The known browser of a bundle id.
public func knownBrowser(_ bundleID: String) -> KnownBrowser? {
    knownBrowsers.first { $0.bundleID == bundleID }
}

/// AppleScript errors after which the next script of a browser cannot do
/// better: not allowed (-1743), would need consent (-1744), not running (-600).
public func isFinalAppleScriptError(_ code: Int?) -> Bool {
    [-1743, -1744, -600].contains(code ?? 0)
}
