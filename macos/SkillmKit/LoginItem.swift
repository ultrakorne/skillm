import Foundation
import ServiceManagement

/// Whether the app starts at login.
public enum LoginItemState: Equatable, Sendable {
    case enabled
    case disabled
    /// Registered, but the user has to allow it in System Settings →
    /// General → Login Items.
    case requiresApproval
}

/// Start at login. It is the one setting not kept in `config.toml`: the
/// system owns it, and the user can change it in System Settings, so the
/// app reads it again every time Settings opens.
@MainActor
public protocol LoginItemService: AnyObject {
    var state: LoginItemState { get }
    func register() throws
    func unregister() throws
    /// Opens System Settings → General → Login Items.
    func openSystemSettings()
}

/// The app itself as a login item (`SMAppService.mainApp`).
@MainActor
public final class AppLoginItem: LoginItemService {
    public init() {}

    public var state: LoginItemState {
        switch SMAppService.mainApp.status {
        case .enabled: .enabled
        case .requiresApproval: .requiresApproval
        default: .disabled
        }
    }

    public func register() throws { try SMAppService.mainApp.register() }
    public func unregister() throws { try SMAppService.mainApp.unregister() }
    public func openSystemSettings() { SMAppService.openSystemSettingsLoginItems() }
}
