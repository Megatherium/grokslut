#!/usr/bin/env sh
set -eu

: "${ANDROID_HOME:?Set ANDROID_HOME to the Android SDK directory}"
: "${JAVA_HOME:?Set JAVA_HOME to a JDK 17 directory}"

gomobile_version="${GOMOBILE_VERSION:-v0.0.0-20241213221354-a87c1cf6cf46}"
go install "golang.org/x/mobile/cmd/gomobile@$gomobile_version"
gomobile_bin="$(go env GOBIN)"
if [ -z "$gomobile_bin" ]; then
  gomobile_bin="$(go env GOPATH)/bin"
fi
gomobile="$gomobile_bin/gomobile"

"$gomobile" init
mkdir -p android/app/libs
GOFLAGS="${GOFLAGS:+$GOFLAGS }-buildvcs=false" "$gomobile" bind \
  -target=android \
  -androidapi 26 \
  -o android/app/libs/grokcore.aar \
  ./mobile

"${GRADLE:-./gradlew}" :android:app:assembleDebug
