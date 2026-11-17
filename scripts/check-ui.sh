#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
export CI=true
export FLUTTER_SUPPRESS_ANALYTICS=true
(cd packages/edge-link-dart && dart pub get && dart analyze && dart test)
(cd integrations/dsh-flutter-ui && flutter --suppress-analytics pub get && flutter --suppress-analytics analyze && flutter --suppress-analytics test)
