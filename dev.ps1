# MagicWebb dev launcher — loads .env then starts air (hot reload) from backend/
$ErrorActionPreference = 'Stop'

$envFile = Join-Path $PSScriptRoot ".env"
if (-not (Test-Path $envFile)) {
    Write-Error ".env not found at $envFile"
    exit 1
}

# Parse .env: skip blank lines and comments, split on first '=' only
Get-Content $envFile | ForEach-Object {
    $line = $_.Trim()
    if ($line -eq '' -or $line.StartsWith('#')) { return }
    $idx = $line.IndexOf('=')
    if ($idx -lt 0) { return }
    $key = $line.Substring(0, $idx).Trim()
    $val = $line.Substring($idx + 1).Trim()
    [System.Environment]::SetEnvironmentVariable($key, $val, 'Process')
}

# Override FRONTEND_URL — server serves UI + API on the same port
[System.Environment]::SetEnvironmentVariable('FRONTEND_URL', 'http://localhost:8080', 'Process')

Write-Host "Loaded .env  →  RPC=$env:RPC_URL  DB connected  port=$env:HTTP_ADDR" -ForegroundColor Green
Write-Host "Starting air (hot reload)...  open http://localhost:8080" -ForegroundColor Cyan

Set-Location (Join-Path $PSScriptRoot "backend")
air
