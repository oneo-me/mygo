#!/bin/sh
# Runs a go command for the iOS simulator on this Mac, with Xcode's clang:
# Go links iOS only with cgo on, though MyGo imports no C.
#
#   sh scripts/ios.sh go vet ./...
#   sh scripts/ios.sh go test -exec 'xcrun simctl spawn booted' ./internal/ios
set -e
sdk=$(xcrun --sdk iphonesimulator --show-sdk-path)
target="-isysroot $sdk -target $(uname -m)-apple-ios15.0-simulator"
arch=$(go env GOHOSTARCH)
GOOS=ios GOARCH=$arch CGO_ENABLED=1 CC=$(xcrun --sdk iphonesimulator -f clang) \
	CGO_CFLAGS="$target" CGO_LDFLAGS="$target" exec "$@"
