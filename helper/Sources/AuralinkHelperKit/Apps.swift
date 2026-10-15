import Foundation

/// The apps app.list answers: the running ones and the installed ones,
/// each bundle id once (ignoring case; a running app keeps its pid and
/// takes the installed one's path when it has none), by name.
public func mergeApps(running: [AppInfo], installed: [AppInfo]) -> [AppInfo] {
    var out: [AppInfo] = []
    var index: [String: Int] = [:]
    for app in running + installed {
        let key = app.bundleId.lowercased()
        guard !key.isEmpty else { continue }
        if let i = index[key] {
            if out[i].path == nil { out[i].path = app.path }
            continue
        }
        index[key] = out.count
        out.append(app)
    }
    return out.sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
}

/// The index of the last element of roles (ancestors, nearest first) that
/// is role: the outermost web area around the focus, the page rather than
/// one of its frames. nil for none.
public func outermost(_ roles: [String], role: String) -> Int? {
    roles.lastIndex(of: role)
}
