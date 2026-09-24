// Checks that an update archive's EdDSA signature (from the appcast) verifies
// against the public key the app carries (SUPublicEDKey), the check every
// installed app makes before it installs the update. A release signed with
// any other private key would be refused by every installed app, so
// release.sh runs this before anything is published.
//
// Usage: xcrun swift verify-update-signature.swift <archive> <edSignature> <SUPublicEDKey>
import CryptoKit
import Foundation

let args = CommandLine.arguments
guard args.count == 4 else {
    FileHandle.standardError.write(Data("usage: verify-update-signature.swift <archive> <signature> <public-key>\n".utf8))
    exit(2)
}
guard let archive = FileManager.default.contents(atPath: args[1]) else {
    FileHandle.standardError.write(Data("error: cannot read \(args[1])\n".utf8))
    exit(1)
}
guard let signature = Data(base64Encoded: args[2]), signature.count == 64 else {
    FileHandle.standardError.write(Data("error: the signature is not a base64 Ed25519 signature\n".utf8))
    exit(1)
}
guard let keyData = Data(base64Encoded: args[3]), keyData.count == 32,
    let key = try? Curve25519.Signing.PublicKey(rawRepresentation: keyData)
else {
    FileHandle.standardError.write(Data("error: SUPublicEDKey is not a base64 Ed25519 public key\n".utf8))
    exit(1)
}
guard key.isValidSignature(signature, for: archive) else {
    FileHandle.standardError.write(
        Data(
            "error: the update's signature does not match the app's SUPublicEDKey; installed apps would refuse it (wrong Sparkle private key?)\n"
                .utf8))
    exit(1)
}
print("EdDSA signature matches SUPublicEDKey")
