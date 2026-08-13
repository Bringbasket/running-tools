$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot

Push-Location (Join-Path $projectRoot 'frontend')
try {
    npm ci
    npm run typecheck
    npm run test:run
    npm run build
} finally {
    Pop-Location
}

Push-Location $projectRoot
try {
    go test ./...
    go build -tags embed ./cmd/server
} finally {
    Pop-Location
}
