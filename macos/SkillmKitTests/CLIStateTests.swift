import AppKit
import XCTest

@testable import SkillmKit

/// The installed CLI as the app sees it: none installed (Install skillm
/// CLI), too old or too new for the app, and a newer release (Upgrade
/// skillm CLI to X). The CLI is TestSupport/fake-skillm, found by the real
/// lookup in a temporary install folder.
@MainActor
final class CLIStateTests: XCTestCase {
    private var scratch: URL!
    private var bin: URL!
    private var models: [AppModel] = []
    private let center = NotificationCenter()

    override func setUp() async throws {
        scratch = FileManager.default.temporaryDirectory.appending(path: "skillm-cli-state-\(UUID().uuidString)")
        bin = scratch.appending(path: "bin")
        try FileManager.default.createDirectory(at: bin, withIntermediateDirectories: true)
    }

    override func tearDown() async throws {
        for m in models { await m.shutdown() }
        models = []
        try? FileManager.default.removeItem(at: scratch)
    }

    /// The fake's environment.
    private func environment(_ env: [String: String]) -> [String: String] {
        var environment = [
            "FAKE_SKILLM_FIXTURES": TestPaths.fixtures.path,
            "FAKE_SKILLM_LOG": scratch.appending(path: "log").path,
            "PATH": "/usr/bin:/bin",
        ]
        environment.merge(env) { $1 }
        return environment
    }

    /// A model that looks for skillm in `bin` only (no login shell), and
    /// whose install script is `installScript` (it installs into `bin`).
    private func model(_ env: [String: String] = [:], installScript: URL? = nil) -> AppModel {
        let environment = environment(env)
        let bin = self.bin!
        let m = AppModel(
            makeClient: {
                let cli = try await SkillmBinary.locate(
                    environment: [:], debug: false, directories: [bin], loginShell: nil)
                return SkillmClient(executable: cli, environment: environment)
            },
            checksGit: false, timing: .init(interval: .seconds(3600), wakeDelay: .zero), wakeCenter: center,
            installScript: installScript, installEnvironment: ["SKILLM_BIN_DIR": bin.path])
        models.append(m)
        return m
    }

    /// Puts the fake at `bin/skillm`, as install.sh would put skillm there.
    private func installFake() throws {
        try FileManager.default.copyItem(at: TestPaths.fakeSkillm, to: bin.appending(path: "skillm"))
    }

    /// Replaces `bin/skillm` with a script that runs the fake with `env`
    /// added, as an upgrade in a terminal replaces the file.
    private func replaceFake(_ env: [String: String]) throws {
        let vars = env.map { "\($0.key)='\($0.value)'" }.sorted().joined(separator: " ")
        let body = "#!/bin/sh\nexec env \(vars) '\(TestPaths.fakeSkillm.path)' \"$@\"\n"
        let target = bin.appending(path: "skillm")
        try FileManager.default.removeItem(at: target)
        FileManager.default.createFile(
            atPath: target.path, contents: Data(body.utf8), attributes: [.posixPermissions: 0o755])
    }

    /// A stand-in for install.sh.
    private func script(_ body: String) -> URL {
        let url = scratch.appending(path: "install.sh")
        FileManager.default.createFile(atPath: url.path, contents: Data(body.utf8))
        return url
    }

    private func commands() -> [String] {
        let s = (try? String(contentsOf: scratch.appending(path: "log"), encoding: .utf8)) ?? ""
        return s.split(separator: "\n").map(String.init)
    }

    private func eventually(_ what: String, _ condition: () -> Bool) async throws {
        let deadline = ContinuousClock.now + .seconds(10)
        while !condition() {
            guard ContinuousClock.now < deadline else {
                return XCTFail("timed out waiting for \(what); commands: \(commands())")
            }
            try await Task.sleep(for: .milliseconds(20))
        }
    }

    /// What a launch that finds a usable CLI runs.
    private let launch = ["version --json", "status --json", "config get --json", "refresh --if-due --json"]

    // MARK: - Missing

    func testNoCLIOffersToInstallIt() async throws {
        let m = model(installScript: script("cp '\(TestPaths.fakeSkillm.path)' \"$SKILLM_BIN_DIR/skillm\""))
        await m.start()
        XCTAssertEqual(m.cli, .missing)
        XCTAssertNil(m.cliPath)
        XCTAssertNil(m.refresh(), "a command ran without a CLI")
        XCTAssertNil(m.upgradeCLI())

        let install = try XCTUnwrap(m.installCLI())
        XCTAssertEqual(m.cliWork, .installing)
        XCTAssertNil(m.installCLI(), "a second install started")
        await install.value
        await m.waitUntilIdle()
        XCTAssertNil(m.cliWork)
        XCTAssertNil(m.cliProblem)
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"))
        XCTAssertEqual(m.cliPath, bin.appending(path: "skillm"))
        XCTAssertEqual(commands(), launch, "the launch checks ran again, then the status and a scheduled refresh")
    }

    func testAFailedInstallSaysWhy() async throws {
        let m = model(installScript: script("echo 'error: download failed: https://example.invalid' >&2; exit 1"))
        await m.start()
        await m.installCLI()?.value
        XCTAssertEqual(m.cli, .missing)
        XCTAssertEqual(m.cliProblem, "Install failed: download failed: https://example.invalid")
        XCTAssertNil(m.cliWork)
    }

    func testACLIInstalledInATerminalIsFoundAtTheNextTick() async throws {
        let m = model()
        await m.start()
        XCTAssertEqual(m.cli, .missing)
        try installFake()
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the wake's launch check") { m.isReady }
        await m.waitUntilIdle()
        XCTAssertEqual(commands(), launch)
    }

    func testTheUpdaterIsAskedWithoutACLI() async throws {
        let m = model()
        let updater = FakeUpdater()
        m.upgrade.attach(updater)
        await m.start()
        XCTAssertEqual(m.cli, .missing)
        XCTAssertEqual(updater.probes, 1, "an app update is looked for whether or not the CLI works")
    }

    // MARK: - A CLI changed in a terminal

    func testACLIUpgradedInATerminalToATooNewOneIsNoticedAtTheNextTick() async throws {
        try installFake()
        let m = model()
        await m.start()
        await m.waitUntilIdle()
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"))
        XCTAssertNil(m.checkCLIAgain(), "checked again while the CLI is unchanged")

        try replaceFake(["FAKE_SKILLM_MODE": "api99"])
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the tick's launch check") { m.cli == .tooNew(version: "9.0.0") }
        XCTAssertEqual(m.cliVersion, "9.0.0")
    }

    func testACLIRemovedInATerminalIsNoticedAtTheNextTick() async throws {
        try installFake()
        let m = model()
        await m.start()
        await m.waitUntilIdle()
        try FileManager.default.removeItem(at: bin.appending(path: "skillm"))
        center.post(name: NSWorkspace.didWakeNotification, object: nil)
        try await eventually("the tick's launch check") { m.cli == .missing }
        XCTAssertNil(m.cliPath)
    }

    func testACLIReplacedUnderACommandIsCheckedWhenItEnds() async throws {
        try installFake()
        let m = model()
        await m.start()
        await m.waitUntilIdle()
        try replaceFake(["FAKE_SKILLM_MODE": "api99"])
        // The Refresh item ends with the new CLI's answer, which it cannot
        // read; the launch checks follow.
        await m.refresh()?.value
        try await eventually("the launch check") { m.cli == .tooNew(version: "9.0.0") }
    }

    func testCheckAgainFindsACLIInstalledMeanwhile() async throws {
        let m = model()
        await m.start()
        try installFake()
        await m.checkCLIAgain()?.value
        await m.waitUntilIdle()
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"))
        XCTAssertNil(m.checkCLIAgain(), "checked again while the CLI works")
    }

    // MARK: - API version

    func testACLIFromBeforeTheJSONAPIIsTooOldAndUpgradesPlainly() async throws {
        let old = scratch.appending(path: "old")
        FileManager.default.createFile(atPath: old.path, contents: nil)
        try installFake()
        let m = model(["FAKE_SKILLM_OLD_WHILE": old.path])
        await m.start()
        XCTAssertEqual(m.cli, .tooOld(version: ""))
        XCTAssertEqual(m.cliPath, bin.appending(path: "skillm"))
        XCTAssertNil(m.cliVersion)
        XCTAssertNil(m.installCLI(), "installed over an existing CLI")

        await m.upgradeCLI()?.value
        await m.waitUntilIdle()
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"))
        XCTAssertEqual(commands(), ["version --json", "upgrade"] + launch, "a plain upgrade, then the launch checks")
    }

    func testAnOlderAPIVersionIsTooOld() async throws {
        try installFake()
        let m = model(["FAKE_SKILLM_MODE": "api0"])
        await m.start()
        XCTAssertEqual(m.cli, .tooOld(version: "0.3.0"))
        XCTAssertEqual(m.cliVersion, "0.3.0")
    }

    func testAFailedPlainUpgradeSaysWhy() async throws {
        try installFake()
        // api0 answers every command with its version document, and a plain
        // upgrade that writes no protocol still exits 0: make it fail.
        let m = model(["FAKE_SKILLM_MODE": "api0"])
        await m.start()
        try FileManager.default.removeItem(at: bin.appending(path: "skillm"))
        FileManager.default.createFile(
            atPath: bin.appending(path: "skillm").path,
            contents: Data("#!/bin/sh\necho 'error: no release for darwin' >&2\nexit 1\n".utf8),
            attributes: [.posixPermissions: 0o755])
        await m.upgradeCLI()?.value
        XCTAssertEqual(m.cliProblem, "Upgrade failed: no release for darwin")
        XCTAssertNil(m.cliWork)
    }

    func testANewerSchemaAsksForAnAppUpdate() async throws {
        try installFake()
        let m = model(["FAKE_SKILLM_MODE": "schema2"])
        await m.start()
        XCTAssertEqual(m.cli, .tooNew(version: ""))
        XCTAssertNil(m.cliVersion)
    }

    func testAnUpgradeThatLeavesTheCLITooOldSaysSo() async throws {
        try installFake()
        // api0 answers `upgrade` with its version document and exits 0: the
        // latest release is the one installed, still too old.
        let m = model(["FAKE_SKILLM_MODE": "api0"])
        await m.start()
        await m.upgradeCLI()?.value
        XCTAssertEqual(m.cli, .tooOld(version: "0.3.0"))
        XCTAssertEqual(m.cliProblem, AppModel.latestTooOld)
    }

    func testAQuitWaitsForTheInstall() async throws {
        let done = scratch.appending(path: "installed")
        let m = model(installScript: script("/bin/sleep 1; : > '\(done.path)'"))
        await m.start()
        XCTAssertFalse(m.hasRunningCommands)
        let install = try XCTUnwrap(m.installCLI())
        XCTAssertTrue(m.hasRunningCommands, "a quit would not wait for the install")
        await m.shutdown()
        XCTAssertTrue(FileManager.default.fileExists(atPath: done.path), "the quit did not wait for the install")
        await install.value
        XCTAssertFalse(m.hasRunningCommands)
    }

    func testANewerAPIVersionAsksForAnAppUpdate() async throws {
        try installFake()
        let m = model(["FAKE_SKILLM_MODE": "api99"])
        await m.start()
        XCTAssertEqual(m.cli, .tooNew(version: "9.0.0"))
        XCTAssertEqual(m.cliVersion, "9.0.0")
        XCTAssertNil(m.upgradeCLI(), "upgraded a CLI that is already too new")
        XCTAssertFalse(m.upgrade.canCheck)
        let updater = FakeUpdater()
        m.upgrade.attach(updater)
        XCTAssertTrue(m.upgrade.canCheck)
        m.upgrade.upgrade()
        XCTAssertEqual(updater.installs, 1)
    }

    // MARK: - A newer release

    /// status.json says 0.5.0 is out and `skillm upgrade` can install it.
    /// The launch tick's refresh fails, so that status stays.
    private func startedWithANewerRelease() async throws -> AppModel {
        try installFake()
        let m = model(["FAKE_SKILLM_FAIL_ON": "refresh"])
        await m.start()
        await m.waitUntilIdle()
        return m
    }

    func testANewerReleaseOffersTheCLIUpgradeAndTheDot() async throws {
        let m = try await startedWithANewerRelease()
        XCTAssertEqual(m.cliUpgrade, "0.5.0")
        XCTAssertTrue(m.badge)
    }

    func testUpgradingTheCLIRereadsItsVersionAndTheStatus() async throws {
        let m = try await startedWithANewerRelease()
        let before = commands().count
        let task = try XCTUnwrap(m.upgradeCLI())
        XCTAssertEqual(m.activity, .upgradingCLI)
        XCTAssertNil(m.refresh(), "a command started while the CLI upgrades")
        await task.value
        XCTAssertEqual(Array(commands().dropFirst(before)), ["upgrade --json", "version --json", "status --json"])
        XCTAssertEqual(m.notice, .init(text: "Upgraded skillm CLI to 0.5.0", isError: false))
        XCTAssertEqual(m.cli, .ready(version: "0.4.0"), "the version the CLI reports now")
    }

    func testNoUpgradeItemWithoutANewerRelease() async throws {
        try installFake()
        let m = model()
        await m.start()
        await m.waitUntilIdle()
        // refresh.json: the release lookup failed.
        XCTAssertNil(m.cliUpgrade)
        XCTAssertNil(m.upgradeCLI())
    }
}
