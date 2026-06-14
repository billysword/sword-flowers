#!/bin/bash
set -euo pipefail

# Load .env, then run the server. set -a exports every var defined by source.
set -a
source "$(dirname "$0")/.env"
set +a

go run .
