param([string]$Name = ('trial-' + (Get-Date -Format 'yyyyMMdd-HHmmss')))
$ErrorActionPreference = 'Stop'
if ($Name -notmatch '^[a-zA-Z0-9_-]+$') { throw 'Name must contain only letters, numbers, - or _.' }
$root = Split-Path $PSScriptRoot -Parent
$output = Join-Path $root ".local/portable/$Name"
if (Test-Path -LiteralPath $output) { throw "Output already exists: $output" }
$env:GOTOOLCHAIN = 'local'
$env:CGO_ENABLED = '0'
$env:GOPROXY = 'off'
$env:GOFLAGS = '-mod=readonly'
Push-Location $root
try {
    New-Item -ItemType Directory -Path $output | Out-Null
    $compiler = Join-Path $output 'compiler'
    & go tool mygo build -platform windows/amd64 -skip-installer -o $compiler
    if ($LASTEXITCODE -ne 0) { throw "MyGo build failed: $LASTEXITCODE" }
    $payload = Join-Path $output 'SyncMaid'
    New-Item -ItemType Directory -Path $payload | Out-Null
    Copy-Item -LiteralPath (Join-Path $compiler 'windows-amd64/SyncMaid.exe') -Destination $payload
    Copy-Item -LiteralPath (Join-Path $root 'docs/portable-trial.txt') -Destination (Join-Path $payload '使用说明.txt')
    $licenses = Join-Path $payload 'licenses'
    New-Item -ItemType Directory -Path $licenses | Out-Null
    Copy-Item -LiteralPath (Join-Path $root 'assets/porcelain-icons/LICENSE') -Destination (Join-Path $licenses 'Lucide-ISC.txt')
    $goroot = & go env GOROOT
    if ($LASTEXITCODE -ne 0) { throw 'Cannot locate Go license.' }
    Copy-Item -LiteralPath (Join-Path $goroot 'LICENSE') -Destination (Join-Path $licenses 'Go-LICENSE.txt')
    $modules = & go list -m -f '{{if .Dir}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' all
    if ($LASTEXITCODE -ne 0) { throw 'Cannot list cached module licenses.' }
    foreach ($line in $modules) {
        if (-not $line) { continue }
        $parts = $line.Split('|')
        if (-not $parts[1]) { continue } # Main module has no third-party license.
        $dest = Join-Path $licenses (($parts[0] -replace '[^a-zA-Z0-9.-]', '_') + '@' + $parts[1])
        New-Item -ItemType Directory -Path $dest | Out-Null
        $files = @(Get-ChildItem -LiteralPath $parts[2] -File | Where-Object { $_.Name -match '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS|COPYRIGHT)(\.|$)' })
        if ($files.Count -eq 0) { throw "Missing license for $($parts[0])" }
        foreach ($file in $files) { Copy-Item -LiteralPath $file.FullName -Destination $dest }
    }
    $zip = Join-Path $output 'SyncMaid-windows-amd64-trial.zip'
    Compress-Archive -LiteralPath $payload -DestinationPath $zip
    Write-Output "Trial directory: $payload"
    Write-Output "Trial ZIP: $zip"
} finally { Pop-Location }
