#!/usr/bin/env sh
set -eu

: "${ANDROID_HOME:?Set ANDROID_HOME to the Android SDK directory}"
: "${JAVA_HOME:?Set JAVA_HOME to a JDK 17 directory}"

go install golang.org/x/mobile/cmd/gomobile@latest
"$(go env GOPATH)/bin/gomobile" init
mkdir -p android/app/libs
"$(go env GOPATH)/bin/gomobile" bind \
  -target=android \
  -androidapi 26 \
  -o android/app/libs/grokcore.aar \
  ./mobile

"${GRADLE:-gradle}" :android:app:assembleDebug
