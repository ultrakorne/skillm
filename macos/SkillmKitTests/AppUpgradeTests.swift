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

/// "Upgrade app and restart": when the updater is asked, when the item
/// shows, what it does, and the feed the Info.plist names.
@MainActor
final class AppUpgradeTests: XCTestCase {
    // MARK: - UpdateFeed

    private let key = Data(repeating: 7, count: 32).base64EncodedString()

    func testFeedNeedsAURLAndAnEd25519Key() {
        let url = "https://github.com/ultrakorne/skillm/releases/download/macos-appcast/appcast.xml"
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

    private let start = Date(timeIntervalSince1970: 1_800_000_000)
    private let day: TimeInterval = 24 * 3600

    func testTheUpdaterIsAskedOncePerInterval() {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        XCTAssertEqual(updater.probes, 0, "attaching asks nothing: the model's next tick does")

        upgrade.tick(now: start, every: day)
        XCTAssertEqual(updater.probes, 1, "never asked yet")
        upgrade.tick(now: start.addingTimeInterval(3600), every: day)
        XCTAssertEqual(updater.probes, 1, "asked again within the interval")
        XCTAssertFalse(upgrade.isAvailable, "shown before the updater found the update")

        upgrade.notFound()
        upgrade.tick(now: start.addingTimeInterval(day), every: day)
        XCTAssertEqual(updater.probes, 2, "the interval passed")
    }

    func testAutoCheckOffAsksOnlyForTheRefreshItem() {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)
        upgrade.tick(now: start, every: nil)
        XCTAssertEqual(updater.probes, 0)
        upgrade.checkNow(now: start)
        upgrade.checkNow(now: start)
        XCTAssertEqual(updater.probes, 2, "the Refresh item asks every time")
    }

    func testTheItemShowsOnceTheUpdaterFoundAnUpdate() {
        let upgrade = AppUpgrade()
        upgrade.found(version: "0.5.0")
        XCTAssertFalse(upgrade.isAvailable, "no updater (a debug build)")

        let updater = FakeUpdater()
        upgrade.attach(updater)
        XCTAssertTrue(upgrade.isAvailable)
        XCTAssertEqual(upgrade.version, "0.5.0")
        upgrade.tick(now: start, every: day)
        upgrade.checkNow(now: start)
        XCTAssertEqual(updater.probes, 0, "asked again after it found the update")

        upgrade.upgrade()
        XCTAssertEqual(updater.installs, 1)
        // Skip This Version changes nothing: the item still installs it.
        XCTAssertTrue(upgrade.isAvailable)
    }

    func testACheckThatDidNotStartOrFailedIsAskedAgainAtTheNextTick() {
        let upgrade = AppUpgrade()
        let updater = FakeUpdater()
        upgrade.attach(updater)

        // Busy: no check started, so the next tick asks again.
        updater.starts = false
        upgrade.tick(now: start, every: day)
        updater.starts = true
        upgrade.tick(now: start.addingTimeInterval(60), every: day)
        XCTAssertEqual(updater.probes, 2)
        upgrade.tick(now: start.addingTimeInterval(120), every: day)
        XCTAssertEqual(updater.probes, 2, "a check that started is not asked again")

        // Offline, or no appcast yet.
        upgrade.probeFailed()
        upgrade.tick(now: start.addingTimeInterval(180), every: day)
        XCTAssertEqual(updater.probes, 3)
    }

    func testCheckForAppUpdateNeedsAnUpdater() {
        let upgrade = AppUpgrade()
        XCTAssertFalse(upgrade.canCheck, "a debug build does not update itself")
        let updater = FakeUpdater()
        upgrade.attach(updater)
        XCTAssertTrue(upgrade.canCheck)
        upgrade.upgrade()
        XCTAssertEqual(updater.installs, 1, "the updater's own window checks")
    }
}
