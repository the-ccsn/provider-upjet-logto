#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' 'This repository is already configured for Logto.' 'Use make generate or make sync-provider; template renaming is disabled.' >&2
exit 1
