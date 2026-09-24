import XCTest

@testable import SkillmKit

/// A login item the test drives.
@MainActor
final class FakeLoginItem: LoginItemService {
    var state: LoginItemState = .disabled
    /// What `register` leaves.
    var stateAfterRegister: LoginItemState = .enabled
    var failure: (any Error)?
    var openedSettings = false

    func register() throws {
        if let failure { throw failure }
        state = stateAfterRegister
    }

    func unregister() throws {
        if let failure { throw failure }
        state = .disabled
    }

    func openSystemSettings() { openedSettings = true }
}

/// The Settings window's model against the fake: settings and agents, the
/// disable confirmation, the refresh interval, Start at login and the
/// command-line tool.
@MainActor
final class SettingsModelTests: XCTestCase {
    private var harness: FakeHarness!
    private var loginItem: FakeLoginItem!
    private var bin: URL!
    /// A stand-in for install.sh: puts an executable skillm in
    /// `$SKILLM_BIN_DIR` and records the version it was asked for.
    private var installScript: URL!

    override func tearDown() async throws {
        await harness?.tearDown()
        harness = nil
        if let bin { try? FileManager.default.removeItem(at: bin) }
        if let installScript { try? FileManager.default.removeItem(at: installScript) }
    }

    private func started(_ env: [String: String] = [:]) async throws -> SettingsModel {
        harness = try FakeHarness(env)
        try await harness.start()
        loginItem = FakeLoginItem()
        bin = FileManager.default.temporaryDirectory.appending(path: "skillm-bin-\(UUID().uuidString)")
        installScript = FileManager.default.temporaryDirectory.appending(path: "install-\(UUID().uuidString).sh")
        FileManager.default.createFile(
            atPath: installScript.path,
            contents: Data("""
                mkdir -p "$SKILLM_BIN_DIR"
                printf '%s' "${SKILLM_VERSION:-latest}" > "$SKILLM_BIN_DIR/version"
                printf '#!/bin/sh\\n' > "$SKILLM_BIN_DIR/skillm" && chmod +x "$SKILLM_BIN_DIR/skillm"
                """.utf8))
        return SettingsModel(
            app: harness.app, loginItem: loginItem, toolDirectories: [bin],
            installScript: installScript, installEnvironment: ["SKILLM_BIN_DIR": bin.path])
    }

    func testLoadReadsTheSettingsAndAgents() async throws {
        let m = try await started()
        loginItem.state = .requiresApproval
        await m.load()
        XCTAssertEqual(harness.commands(), ["config get --json", "agent ls --json"])
        XCTAssertEqual(m.agents.map(\.name), ["agents", "claude", "opencode"])
        XCTAssertEqual(harness.app.settings, RefreshSettings(enabled: true, intervalHours: 12))
        XCTAssertEqual(m.loginItemState, .requiresApproval)
        XCTAssertNil(m.loadError)
        XCTAssertNil(m.commandLineTool)
    }

    func testEnablingAnAgentRunsAtOnce() async throws {
        let m = try await started()
        await m.load()
        m.setAgent("opencode", enabled: true)
        XCTAssertNil(m.pendingDisable)
        await harness.app.waitUntilIdle()
        XCTAssertEqual(
            Array(harness.commands().suffix(2)), ["agent set --enable opencode --json", "status --json"])
        // agents_set.json: agents and opencode enabled, claude disabled.
        XCTAssertEqual(m.agents.filter(\.enabled).map(\.name), ["agents", "opencode"])
        XCTAssertEqual(m.message?.isError, true, "its warning is shown")
    }

    func testDisablingAnAgentAsksFirst() async throws {
        let m = try await started()
        await m.load()
        m.setAgent("claude", enabled: false)
        XCTAssertEqual(m.pendingDisable, "claude")
        XCTAssertEqual(harness.commands(), ["config get --json", "agent ls --json"])
        await m.confirmDisable()?.value
        XCTAssertNil(m.pendingDisable)
        XCTAssertTrue(harness.commands().contains("agent set --disable claude --yes --json"))
    }

    /// A Disable confirmed while another command runs is not lost: the
    /// question stays until the disable can start.
    func testADisableWaitsForTheRunningCommand() async throws {
        let m = try await started(["FAKE_SKILLM_HANG_ON": "update"])
        await m.load()
        m.setAgent("claude", enabled: false)
        let update = harness.app.updateAll()
        XCTAssertNotNil(update)
        XCTAssertNil(m.confirmDisable())
        XCTAssertEqual(m.pendingDisable, "claude")
        harness.app.cancel()
        await update?.value
        await m.confirmDisable()?.value
        XCTAssertNil(m.pendingDisable)
        XCTAssertTrue(harness.commands().contains("agent set --disable claude --yes --json"))
    }

    func testTheLastEnabledAgentCannotBeDisabled() async throws {
        let m = try await started()
        await m.load()
        let agents = m.agents
        XCTAssertFalse(m.isLastEnabled(agents[0]), "two are enabled")
        XCTAssertFalse(m.isLastEnabled(agents[2]), "opencode is disabled")
    }

    func testIntervalIsWrittenThenAScheduledRefreshAsks() async throws {
        let m = try await started()
        await m.setInterval(hours: 48)?.value
        await harness.app.waitUntilIdle()
        XCTAssertEqual(
            harness.commands(),
            ["config set refresh.interval_hours 48 --json", "config get --json", "refresh --if-due --json"])
        XCTAssertNil(m.message)
    }

    func testAutoRefreshToggleFromSettings() async throws {
        let m = try await started()
        await m.setAutoRefresh(false)?.value
        XCTAssertEqual(harness.commands(), ["config set refresh.enabled false --json"])
        XCTAssertEqual(harness.app.settings?.enabled, false)
        XCTAssertNil(m.message)
    }

    func testASettingThatFailsIsShownInSettingsNotTheMenu() async throws {
        let failing = FileManager.default.temporaryDirectory.appending(path: "skillm-failing-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: failing) }
        let m = try await started(["FAKE_SKILLM_FAIL_ON": "config", "FAKE_SKILLM_FAIL_WHILE": failing.path])
        FileManager.default.createFile(atPath: failing.path, contents: nil)
        await m.setInterval(hours: 48)?.value
        await harness.app.waitUntilIdle()
        XCTAssertEqual(m.message?.isError, true)
        XCTAssertNil(harness.app.notice)
        XCTAssertEqual(harness.commands(), ["config set refresh.interval_hours 48 --json"])
    }

    func testStartAtLogin() async throws {
        let m = try await started()
        m.setStartAtLogin(true)
        XCTAssertEqual(m.loginItemState, .enabled)
        m.setStartAtLogin(false)
        XCTAssertEqual(m.loginItemState, .disabled)

        loginItem.stateAfterRegister = .requiresApproval
        m.setStartAtLogin(true)
        XCTAssertEqual(m.loginItemState, .requiresApproval)
        m.openLoginItemsSettings()
        XCTAssertTrue(loginItem.openedSettings)

        loginItem.failure = CocoaError(.featureUnsupported)
        m.setStartAtLogin(false)
        XCTAssertEqual(m.message?.isError, true)
        XCTAssertEqual(m.loginItemState, .requiresApproval, "it shows what the system holds")
    }

    func testInstallCommandLineTool() async throws {
        let m = try await started()
        XCTAssertNil(m.commandLineTool)
        await m.installCommandLineTool()?.value
        let installed = bin.appending(path: "skillm")
        XCTAssertEqual(m.commandLineTool, installed)
        XCTAssertFalse(m.isInstallingTool)
        XCTAssertEqual(m.message?.isError, false)
        // Pinned to the app's CLI version (the fake reports 0.4.0), so the terminal gets the same skillm.
        XCTAssertEqual(try String(contentsOf: bin.appending(path: "version"), encoding: .utf8), "v0.4.0")
        m.refreshLocalState()
        XCTAssertEqual(m.commandLineTool, installed)
    }
}
