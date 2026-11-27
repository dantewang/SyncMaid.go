param(
    [Parameter(Mandatory = $true)]
    [string]$Version,
    [string]$Commit = '',
    [string]$BuildDate = '',
    [string]$OutDir = 'dist'
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$bareVersion = $Version.Trim() -replace '^v', ''
if ($bareVersion -notmatch '^\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?$') {
    throw "Invalid -Version '$Version'. Expected SemVer X.Y.Z or X.Y.Z-prerelease."
}

$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
try {
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    $mygoPath = Join-Path $root 'mygo.json'
    $mygoJson = ([System.IO.File]::ReadAllText($mygoPath, $utf8NoBom)) | ConvertFrom-Json
    $mygoVersion = ([string]$mygoJson.version).Trim() -replace '^v', ''
    if ($mygoVersion -ne $bareVersion) {
        throw "Version mismatch: mygo.json has '$mygoVersion', requested '$bareVersion'."
    }

    if ([string]::IsNullOrWhiteSpace($Commit)) {
        $Commit = (& git rev-parse --short=7 HEAD).Trim()
        if ($LASTEXITCODE -ne 0) { throw 'Cannot determine git commit hash.' }
    }
    if ([string]::IsNullOrWhiteSpace($BuildDate)) {
        $BuildDate = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    }

    $outPath = if ([System.IO.Path]::IsPathRooted($OutDir)) { $OutDir } else { Join-Path $root $OutDir }
    New-Item -ItemType Directory -Path $outPath -Force | Out-Null

    $stageDir = Join-Path $outPath ('.stage-' + $bareVersion + '-' + [System.Guid]::NewGuid().ToString('N').Substring(0, 8))
    New-Item -ItemType Directory -Path $stageDir -Force | Out-Null

    try {
        $env:GOTOOLCHAIN = 'local'
        $env:CGO_ENABLED = '0'
        $env:MYGO_LDFLAGS = "-X syncmaid/internal/buildinfo.Version=v$bareVersion -X syncmaid/internal/buildinfo.Commit=$Commit -X syncmaid/internal/buildinfo.BuildDate=$BuildDate"

        $compilerDir = Join-Path $stageDir 'compiler'
        & go tool mygo build -platform windows/amd64 -skip-installer -o $compilerDir
        if ($LASTEXITCODE -ne 0) { throw "MyGo build failed: $LASTEXITCODE" }

        $exePath = Join-Path $compilerDir 'windows-amd64/SyncMaid.exe'
        if (-not (Test-Path -LiteralPath $exePath)) {
            throw "Expected executable missing: $exePath"
        }

        $payload = Join-Path $stageDir 'SyncMaid'
        New-Item -ItemType Directory -Path $payload | Out-Null
        Copy-Item -LiteralPath $exePath -Destination $payload
        Copy-Item -LiteralPath (Join-Path $root 'README.md') -Destination $payload
        Copy-Item -LiteralPath (Join-Path $root 'README.zh-CN.md') -Destination $payload

        $licenses = Join-Path $payload 'licenses'
        New-Item -ItemType Directory -Path $licenses | Out-Null
        Copy-Item -LiteralPath (Join-Path $root 'assets/porcelain-icons/LICENSE') -Destination (Join-Path $licenses 'Lucide-ISC.txt')

        $goroot = (& go env GOROOT).Trim()
        if ($LASTEXITCODE -ne 0 -or -not $goroot) { throw 'Cannot locate GOROOT.' }
        Copy-Item -LiteralPath (Join-Path $goroot 'LICENSE') -Destination (Join-Path $licenses 'Go-LICENSE.txt')

        $modules = & go list -m -f '{{if .Dir}}{{.Path}}|{{.Version}}|{{.Dir}}{{end}}' all
        if ($LASTEXITCODE -ne 0) { throw 'Cannot list Go module licenses.' }
        foreach ($line in $modules) {
            if (-not $line) { continue }
            $parts = $line.Split('|')
            if (-not $parts[1]) { continue }
            $dest = Join-Path $licenses (($parts[0] -replace '[^a-zA-Z0-9.-]', '_') + '@' + $parts[1])
            New-Item -ItemType Directory -Path $dest | Out-Null
            $files = @(Get-ChildItem -LiteralPath $parts[2] -File | Where-Object { $_.Name -match '^(LICENSE|LICENCE|COPYING|NOTICE|PATENTS|COPYRIGHT)(\.|$)' })
            if ($files.Count -eq 0) { throw "Missing license for $($parts[0])" }
            foreach ($file in $files) {
                Copy-Item -LiteralPath $file.FullName -Destination $dest
            }
        }

        $zipName = "SyncMaid-v$bareVersion-windows-amd64.zip"
        $zipPath = Join-Path $outPath $zipName
        if (Test-Path -LiteralPath $zipPath) {
            Remove-Item -LiteralPath $zipPath -Force
        }
        Compress-Archive -LiteralPath $payload -DestinationPath $zipPath

        $sha = (Get-FileHash -LiteralPath $zipPath -Algorithm SHA256).Hash.ToLowerInvariant()
        $sumsPath = Join-Path $outPath 'SHA256SUMS.txt'
        [System.IO.File]::WriteAllText($sumsPath, "$sha  $zipName`n", $utf8NoBom)

        Write-Output "Release package : $zipPath"
        Write-Output "Checksums       : $sumsPath"
    } finally {
        if (Test-Path -LiteralPath $stageDir) {
            Remove-Item -LiteralPath $stageDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
} finally {
    Pop-Location
}
