// Seismo menu bar app (v0.1)
//
// Single-bundle macOS 13+ app that embeds the Go seismo binary as a
// privileged LaunchDaemon, registered via SMAppService. The menu bar icon
// shows live PGA (peak ground acceleration) and flashes on STA/LTA
// triggers. Everything else lives in the browser dashboard.

import Cocoa
import ServiceManagement

let kHelperLabel     = "com.gojaehyeon.seismo.helper"
let kHelperPlistName = "com.gojaehyeon.seismo.helper.plist"
let kHelperURL       = "http://127.0.0.1:8766"

final class AppDelegate: NSObject, NSApplicationDelegate {
    private var statusItem: NSStatusItem!
    private var pollTimer: Timer?
    private var helperService: SMAppService!

    // Menu item tags
    private let tagStatus  = 1
    private let tagEnable  = 2
    private let tagPGA     = 3
    private let tagRMS     = 4
    private let tagSTALTA  = 5
    private let tagQuakes  = 6
    private let tagDisable = 7

    // State
    private var lastQuakeCount = 0
    private var daemonOnline = false
    private var alertFlashUntil = Date.distantPast

    // Icon glyphs
    private let iconIdle    = "◆"
    private let iconOffline = "◇"
    private let iconAlert   = "⚠"

    // MARK: - App lifecycle

    func applicationDidFinishLaunching(_ notification: Notification) {
        helperService = SMAppService.daemon(plistName: kHelperPlistName)

        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = statusItem.button {
            button.title = iconOffline
            button.font = NSFont.monospacedSystemFont(ofSize: 13, weight: .regular)
            button.toolTip = "seismo — live MacBook seismograph"
        }
        buildMenu()
        startPolling()
    }

    // MARK: - Menu

    private func buildMenu() {
        let menu = NSMenu()
        menu.autoenablesItems = false

        let title = NSMenuItem(title: "seismo", action: nil, keyEquivalent: "")
        title.isEnabled = false
        menu.addItem(title)

        let status = NSMenuItem(title: "status: starting…", action: nil, keyEquivalent: "")
        status.tag = tagStatus
        status.isEnabled = false
        menu.addItem(status)

        let enable = NSMenuItem(title: "enable helper…",
                                action: #selector(enableHelper),
                                keyEquivalent: "")
        enable.target = self
        enable.tag = tagEnable
        menu.addItem(enable)

        menu.addItem(.separator())

        // Live readouts (disabled, just for display)
        let pga = NSMenuItem(title: "PGA: —", action: nil, keyEquivalent: "")
        pga.tag = tagPGA
        pga.isEnabled = false
        menu.addItem(pga)

        let rms = NSMenuItem(title: "RMS: —", action: nil, keyEquivalent: "")
        rms.tag = tagRMS
        rms.isEnabled = false
        menu.addItem(rms)

        let stalta = NSMenuItem(title: "STA/LTA: —", action: nil, keyEquivalent: "")
        stalta.tag = tagSTALTA
        stalta.isEnabled = false
        menu.addItem(stalta)

        let quakes = NSMenuItem(title: "events: 0", action: nil, keyEquivalent: "")
        quakes.tag = tagQuakes
        quakes.isEnabled = false
        menu.addItem(quakes)

        menu.addItem(.separator())

        let reset = NSMenuItem(title: "reset PGA",
                               action: #selector(resetPGA),
                               keyEquivalent: "r")
        reset.target = self
        menu.addItem(reset)

        let dash = NSMenuItem(title: "open dashboard…",
                              action: #selector(openDashboard),
                              keyEquivalent: "d")
        dash.target = self
        menu.addItem(dash)

        menu.addItem(.separator())

        let settings = NSMenuItem(title: "open Login Items settings…",
                                  action: #selector(openLoginItems),
                                  keyEquivalent: "")
        settings.target = self
        menu.addItem(settings)

        let disable = NSMenuItem(title: "disable helper",
                                 action: #selector(disableHelper),
                                 keyEquivalent: "")
        disable.target = self
        disable.tag = tagDisable
        menu.addItem(disable)

        menu.addItem(.separator())

        let quit = NSMenuItem(title: "quit seismo",
                              action: #selector(NSApplication.terminate(_:)),
                              keyEquivalent: "q")
        menu.addItem(quit)

        statusItem.menu = menu
    }

    // MARK: - SMAppService

    @objc private func enableHelper() {
        if helperService.status == .enabled {
            showAlert("already enabled",
                      "The seismo helper is already registered and running.")
            return
        }

        do {
            try helperService.register()
        } catch {
            showAlert("couldn't register helper",
                      """
                      \(error.localizedDescription)

                      Try: System Settings → General → Login Items & Extensions, \
                      find "Seismo" and toggle it on.
                      """,
                      openSettings: true)
            return
        }

        switch helperService.status {
        case .enabled:
            showAlert("helper enabled",
                      "The seismo helper is now running as a LaunchDaemon. Open the dashboard to see the live trace!")
        case .requiresApproval:
            showAlert("approval needed",
                      """
                      macOS is asking you to approve the seismo helper.

                      Open System Settings → General → Login Items & Extensions \
                      and toggle "Seismo" on.
                      """,
                      openSettings: true)
        default:
            showAlert("status: \(statusName(helperService.status))",
                      "Unexpected state. Check Console.app for 'seismo' if it does not start.",
                      openSettings: true)
        }
        fetchState()
    }

    @objc private func disableHelper() {
        do {
            try helperService.unregister()
            showAlert("helper disabled", "The seismo helper has been stopped and unregistered.")
        } catch {
            showAlert("couldn't unregister", error.localizedDescription)
        }
        fetchState()
    }

    @objc private func openLoginItems() {
        let url = URL(string: "x-apple.systempreferences:com.apple.LoginItems-Settings.extension")!
        NSWorkspace.shared.open(url)
    }

    @objc private func openDashboard() {
        if let url = URL(string: "\(kHelperURL)/") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func resetPGA() {
        guard let url = URL(string: "\(kHelperURL)/reset") else { return }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        URLSession.shared.dataTask(with: req) { [weak self] _, _, _ in
            DispatchQueue.main.async { self?.fetchState() }
        }.resume()
    }

    private func statusName(_ s: SMAppService.Status) -> String {
        switch s {
        case .notRegistered:    return "not registered"
        case .enabled:          return "enabled"
        case .requiresApproval: return "requires approval"
        case .notFound:         return "not found"
        @unknown default:       return "unknown"
        }
    }

    private func showAlert(_ title: String,
                           _ info: String,
                           openSettings: Bool = false) {
        NSApp.activate(ignoringOtherApps: true)
        let a = NSAlert()
        a.messageText = title
        a.informativeText = info
        a.addButton(withTitle: "OK")
        if openSettings {
            a.addButton(withTitle: "Open System Settings")
        }
        let res = a.runModal()
        if openSettings && res == .alertSecondButtonReturn {
            openLoginItems()
        }
    }

    // MARK: - Polling

    private func startPolling() {
        // 500ms refresh — fast enough to feel live for peak updates.
        pollTimer = Timer.scheduledTimer(withTimeInterval: 0.5, repeats: true) { [weak self] _ in
            self?.fetchState()
        }
        pollTimer?.tolerance = 0.1
        fetchState()
    }

    private func fetchState() {
        // Pass decim=60 so we don't pull 6000 samples per poll.
        guard let url = URL(string: "\(kHelperURL)/state?decim=60") else { return }
        var req = URLRequest(url: url)
        req.timeoutInterval = 0.8
        URLSession.shared.dataTask(with: req) { [weak self] data, _, err in
            guard let self = self else { return }
            if err != nil || data == nil {
                DispatchQueue.main.async { self.markOffline() }
                return
            }
            if let json = try? JSONSerialization.jsonObject(with: data!) as? [String: Any] {
                DispatchQueue.main.async { self.applyState(json) }
            } else {
                DispatchQueue.main.async { self.markOffline() }
            }
        }.resume()
    }

    private func markOffline() {
        daemonOnline = false
        statusItem.button?.title = iconOffline

        let s = helperService.status
        let text: String
        switch s {
        case .notRegistered:    text = "status: helper not installed"
        case .enabled:          text = "status: helper loading…"
        case .requiresApproval: text = "status: approval required"
        case .notFound:         text = "status: helper binary missing"
        @unknown default:       text = "status: unknown"
        }
        statusItem.menu?.item(withTag: tagStatus)?.title = text

        statusItem.menu?.item(withTag: tagEnable)?.isHidden  = (s == .enabled)
        statusItem.menu?.item(withTag: tagDisable)?.isHidden = (s != .enabled)
    }

    private func applyState(_ json: [String: Any]) {
        daemonOnline = true
        let stats = json["stats"] as? [String: Any] ?? [:]
        let quakes = json["quakes"] as? [[String: Any]] ?? []

        let pga   = stats["pga"]     as? Double ?? 0
        let rms   = stats["rms"]     as? Double ?? 0
        let ratio = stats["sta_lta"] as? Double ?? 0
        let trig  = stats["trigger"] as? Double ?? 4

        statusItem.menu?.item(withTag: tagStatus)?.title = "status: running"
        statusItem.menu?.item(withTag: tagEnable)?.isHidden = true
        statusItem.menu?.item(withTag: tagDisable)?.isHidden = false

        statusItem.menu?.item(withTag: tagPGA)?.title =
            String(format: "PGA: %.4fg", pga)
        statusItem.menu?.item(withTag: tagRMS)?.title =
            String(format: "RMS: %.5fg", rms)
        statusItem.menu?.item(withTag: tagSTALTA)?.title =
            String(format: "STA/LTA: %.2f  (trig %.1f×)", ratio, trig)
        statusItem.menu?.item(withTag: tagQuakes)?.title =
            "events: \(quakes.count)"

        // Flash icon on new event.
        if quakes.count > lastQuakeCount {
            alertFlashUntil = Date().addingTimeInterval(0.6)
        }
        lastQuakeCount = quakes.count

        let now = Date()
        if now < alertFlashUntil {
            statusItem.button?.title = iconAlert + String(format: " %.3fg", pga)
        } else {
            // Compact live PGA next to the idle icon.
            statusItem.button?.title = iconIdle + String(format: " %.3fg", pga)
        }
    }
}

// MARK: - main

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
