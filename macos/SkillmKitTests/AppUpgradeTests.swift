import XCTest

@testable import SkillmKit

/// A stand-in for Sparkle that counts what it was asked.
@MainActor
final class FakeUpdater: AppUpdater {
    var probes = 0
    var installs = 0
    /// False: the updater is busy and starts no check.
    var starts = true
    func probe() -> Bool {
        probes += 1
        return starts
    }
    func install() { installs += 1 }
}

/// "Upgrade and restart": when the updater is asked, when the item shows,
/// what it does, and the feed the Info.plist names.
@MainActor
final class AppUpgradeTests: XCTestCase {
    private func status(_ name: String = "status.json") throws -> StatusData {
        let data = try Data(contentsOf: TestPaths.fixtures.appending(path: name))
        return try XCTUnwrap(protocolDecoder().decode(Envelope<StatusData>.self, from: data).data)
    }

    // MARK: - UpdateFeed

    private let key = Data(repeating: 7, count: 32).base64EncodedString()

    func testFeedNeedsAURLAndAnEd25519Key() {
        let url = "https://github.com/ultrakorne/skillm/releases/latest/download/appcast.xml"
        let feed = UpdateFeed(info: ["SUFeedURL": url, "SUPublicEDKey": key])
        XCTAssertEqual(feed?.url.absoluteString, url)
        XCTAssertEqual(feed?.publicKey, key)

        XCTAssertNil(UpdateFeed(info: nil))
        XCTAssertNil(UpdateFeed(info: ["SUPublicEDKey": key]), "no feed URL")
        XCTAssertNil(UpdateFeed(info: ["SUFeedURL": "", "SUPublicEDKey": key]), "a debug build's empty URL")
        XCTAssertNil(UpdateFeed(info: ["SUFeedURL": url]), "no key")
        XCTAssertNil(UpdateFeed(info: ["SUFeedURL": url, "SUPublicEDKey": "PLACEHOLDER"]), "a placeholder key")
        XCTAssertNil(
            UpdateFeed(info: ["SUFeedURL": url, "SUPublicEDKey": Data(count: 16).base64EncodedString()]),
            "a key of the wrong length")
        XCTAssertNil(UpdateFeed(info: ["SUFeedURL": "$(SKILLM_UPDATE_FEED_URL)", "SUPublicEDKey": key]), "unexpanded")
    }

    // MARK: - AppUpgrade

    func testANewerSkillmAsksTheUpdaterOncePerCheck() throws {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        let newer = try status()
        XCTAssertEqual(newer.cache.selfStatus?.available, true)

        upgrade.statusChanged(newer)
        XCTAssertEqual(updater.probes, 1)
        upgrade.statusChanged(newer)
        XCTAssertEqual(updater.probes, 1, "a re-read of the same cache asks again")
        XCTAssertFalse(upgrade.isAvailable, "shown before the updater found the update")

        // The appcast lagged behind the release: the next check asks again.
        upgrade.notFound()
        var later = newer
        later.cache.checkedAt = newer.cache.checkedAt?.addingTimeInterval(3600)
        upgrade.statusChanged(later)
        XCTAssertEqual(updater.probes, 2)
    }

    func testNothingNewerAsksNothing() throws {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        var current = try status()
        current.cache.selfStatus?.available = false
        upgrade.statusChanged(current)
        upgrade.statusChanged(try status("status_never.json"))
        upgrade.statusChanged(nil)
        XCTAssertEqual(updater.probes, 0)
    }

    func testAnUpdaterAttachedLaterIsAskedAboutTheLastStatus() throws {
        let upgrade = AppUpgrade()
        upgrade.statusChanged(try status())
        let updater = FakeUpdater()
        upgrade.attach(updater)
        XCTAssertEqual(updater.probes, 1)
    }

    func testTheItemShowsOnceTheUpdaterFoundAnUpdate() throws {
        let upgrade = AppUpgrade()
        upgrade.found(version: "0.5.0")
        XCTAssertFalse(upgrade.isAvailable, "no updater (a debug build)")

        let updater = FakeUpdater()
        upgrade.attach(updater)
        XCTAssertTrue(upgrade.isAvailable)
        XCTAssertEqual(upgrade.version, "0.5.0")
        upgrade.statusChanged(try status())
        XCTAssertEqual(updater.probes, 0, "asked again after it found the update")

        upgrade.upgrade()
        XCTAssertEqual(updater.installs, 1)
        // Skip This Version changes nothing: the item still installs it.
        XCTAssertTrue(upgrade.isAvailable)
    }

    func testACheckThatDidNotStartOrFailedIsAskedAgain() throws {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        let newer = try status()

        // Busy: no check started, so the same cache asks again.
        updater.starts = false
        upgrade.statusChanged(newer)
        updater.starts = true
        upgrade.statusChanged(newer)
        XCTAssertEqual(updater.probes, 2)
        upgrade.statusChanged(newer)
        XCTAssertEqual(updater.probes, 2, "a check that started is not asked again")

        // Offline, or no appcast yet.
        upgrade.probeFailed()
        upgrade.statusChanged(newer)
        XCTAssertEqual(updater.probes, 3)
    }

    func testAFailedReleaseLookupAsksTheUpdaterForTheBundledCLIOnly() throws {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        var failed = try status()
        failed.cache.selfStatus?.available = false
        failed.cache.selfStatus?.latest = nil
        failed.cache.selfStatus?.error = "GitHub rate limit"

        failed.cache.selfStatus?.method = .binary
        upgrade.statusChanged(failed)
        XCTAssertEqual(updater.probes, 0, "a CLI outside the app")

        failed.cache.selfStatus?.method = .bundled
        upgrade.statusChanged(failed)
        XCTAssertEqual(updater.probes, 1)
    }
}
