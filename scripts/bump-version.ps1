param(
    [string]$Version = '',
    [switch]$DryRun,
    [switch]$Push,
    [switch]$SkipTests
)

$ErrorActionPreference = 'Stop'

function Parse-SemVer {
    param([string]$Value)
    $trimmed = $Value.Trim() -replace '^v', ''
    if ($trimmed -notmatch '^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$') {
        return $null
    }
    return [pscustomobject]@{
        Raw        = $trimmed
        Major      = [int]$Matches[1]
        Minor      = [int]$Matches[2]
        Patch      = [int]$Matches[3]
        Prerelease = $Matches[4]
    }
}

function Compare-SemVer {
    param($A, $B)
    foreach ($field in @('Major', 'Minor', 'Patch')) {
        if ($A.$field -ne $B.$field) {
            return $A.$field.CompareTo($B.$field)
        }
    }
    $aPre = [string]$A.Prerelease
    $bPre = [string]$B.Prerelease
    if (-not $aPre -and -not $bPre) { return 0 }
    if (-not $aPre) { return 1 }
    if (-not $bPre) { return -1 }
    return [string]::CompareOrdinal($aPre, $bPre)
}

function Get-LatestSemVerTag {
    $rawTags = @(& git tag -l 'v*')
    if ($LASTEXITCODE -ne 0) {
        throw "git tag failed with exit code $LASTEXITCODE"
    }
    $best = $null
    foreach ($t in $rawTags) {
        $name = $t.Trim()
        if (-not $name) { continue }
        $parsed = Parse-SemVer $name
        if ($null -eq $parsed) { continue }
        if ($null -eq $best -or (Compare-SemVer $parsed $best.SemVer) -gt 0) {
            $best = [pscustomobject]@{
                Tag    = "v$($parsed.Raw)"
                SemVer = $parsed
            }
        }
    }
    return $best
}

function Get-CommitsSince {
    param([string]$BaseTag)
    $range = if ($BaseTag) { "$BaseTag..HEAD" } else { 'HEAD' }
    $raw = (& git log $range '--format=%h%x1f%s%x1f%b%x1e') -join "`n"
    if ($LASTEXITCODE -ne 0) {
        throw "git log failed with exit code $LASTEXITCODE"
    }
    $commits = @()
    foreach ($entry in ($raw -split [char]0x1e)) {
        $trimmed = $entry.Trim()
        if (-not $trimmed) { continue }
        $parts = $trimmed -split [char]0x1f, 3
        $commits += [pscustomobject]@{
            Hash    = $parts[0].Trim()
            Subject = if ($parts.Count -gt 1) { $parts[1].Trim() } else { '' }
            Body    = if ($parts.Count -gt 2) { $parts[2].Trim() } else { '' }
        }
    }
    return $commits
}

function Get-BumpLevel {
    param($Commits)
    $level = 'none'
    $rank = @{ 'none' = 0; 'patch' = 1; 'minor' = 2; 'major' = 3 }
    foreach ($c in $Commits) {
        $isBreaking = ($c.Subject -match '^[a-zA-Z]+(\([^)]+\))?!:') -or ($c.Body -match '(?m)^BREAKING[ -]CHANGE:')
        $candidate = 'none'
        if ($isBreaking) {
            $candidate = 'major'
        } elseif ($c.Subject -match '^feat(\([^)]+\))?:') {
            $candidate = 'minor'
        } elseif ($c.Subject -match '^(fix|perf)(\([^)]+\))?:') {
            $candidate = 'patch'
        }
        if ($rank[$candidate] -gt $rank[$level]) {
            $level = $candidate
        }
    }
    return $level
}

$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
try {
    $mygoPath = Join-Path $root 'mygo.json'
    $utf8NoBom = [System.Text.UTF8Encoding]::new($false)
    $mygoRaw = [System.IO.File]::ReadAllText($mygoPath, $utf8NoBom)
    $mygoJson = $mygoRaw | ConvertFrom-Json
    $currentSemVer = Parse-SemVer ([string]$mygoJson.version)
    if ($null -eq $currentSemVer) {
        throw "Invalid version in mygo.json: $($mygoJson.version)"
    }

    $latest = Get-LatestSemVerTag
    $baseLabel = if ($latest) { $latest.Tag } else { '<none>' }
    $commits = @(Get-CommitsSince $(if ($latest) { $latest.Tag } else { '' }))

    $bumpLevel = 'explicit'
    if (-not [string]::IsNullOrWhiteSpace($Version)) {
        $targetSemVer = Parse-SemVer $Version
        if ($null -eq $targetSemVer) {
            throw "Invalid -Version '$Version'. Expected SemVer X.Y.Z or X.Y.Z-prerelease."
        }
        $targetVersion = $targetSemVer.Raw
    } elseif ($null -eq $latest) {
        $bumpLevel = 'initial'
        $targetVersion = $currentSemVer.Raw
    } else {
        if ($commits.Count -eq 0) {
            Write-Output "No new commits since $baseLabel. Nothing to release."
            exit 0
        }
        $bumpLevel = Get-BumpLevel $commits
        switch ($bumpLevel) {
            'major' { $targetVersion = "$($latest.SemVer.Major + 1).0.0" }
            'minor' { $targetVersion = "$($latest.SemVer.Major).$($latest.SemVer.Minor + 1).0" }
            'patch' { $targetVersion = "$($latest.SemVer.Major).$($latest.SemVer.Minor).$($latest.SemVer.Patch + 1)" }
            'none' {
                Write-Output "Base tag: $baseLabel ($($commits.Count) commit(s), none are feat/fix/perf/breaking)."
                Write-Output 'No release bump needed. Pass -Version X.Y.Z to force a release.'
                exit 0
            }
        }
    }

    $targetTag = "v$targetVersion"
    Write-Output "Base tag       : $baseLabel"
    Write-Output "mygo.json      : $($currentSemVer.Raw)"
    Write-Output "Bump level     : $bumpLevel"
    Write-Output "Target release : $targetTag"
    Write-Output "Commits in scope ($($commits.Count)):"
    foreach ($c in ($commits | Select-Object -First 20)) {
        Write-Output "  - $($c.Hash) $($c.Subject)"
    }
    if ($commits.Count -gt 20) {
        Write-Output "  ... ($($commits.Count - 20) more)"
    }

    if ($DryRun) {
        Write-Output '[DryRun] No files modified, no commit or tag created.'
        exit 0
    }

    $status = @(& git status --porcelain)
    if ($LASTEXITCODE -ne 0) { throw 'git status failed.' }
    if ($status.Count -gt 0) {
        throw 'Working tree is not clean. Commit or stash changes before running a release bump.'
    }

    $branch = ((& git rev-parse --abbrev-ref HEAD) -join '').Trim()
    if ($LASTEXITCODE -ne 0) { throw 'git rev-parse failed.' }
    if ($branch -ne 'master') {
        throw "Releases must be cut from 'master' (current branch: '$branch')."
    }

    $existingTag = @(& git tag -l $targetTag | Where-Object { $_ -and $_.Trim() -eq $targetTag })
    if ($LASTEXITCODE -ne 0) { throw 'git tag check failed.' }
    if ($existingTag.Count -gt 0) {
        throw "Tag $targetTag already exists."
    }

    $versionChanged = $currentSemVer.Raw -ne $targetVersion
    if ($versionChanged) {
        $updatedRaw = [regex]::Replace($mygoRaw, '("version"\s*:\s*")[^"]*(")', "`${1}$targetVersion`$2", 1)
        $verifyJson = $updatedRaw | ConvertFrom-Json
        if ($verifyJson.version -ne $targetVersion) {
            throw "Failed to update version field in mygo.json cleanly."
        }
        [System.IO.File]::WriteAllText($mygoPath, $updatedRaw, $utf8NoBom)
        Write-Output "Updated mygo.json version to $targetVersion"
    }

    if (-not $SkipTests) {
        $env:CGO_ENABLED = '0'
        $env:GOTOOLCHAIN = 'local'
        Write-Output 'Running go vet ./...'
        & go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "go vet failed: $LASTEXITCODE" }
        Write-Output 'Running go test -count=1 ./...'
        & go test -count=1 ./...
        if ($LASTEXITCODE -ne 0) { throw "go test failed: $LASTEXITCODE" }
    }

    if ($versionChanged) {
        & git add -- mygo.json
        if ($LASTEXITCODE -ne 0) { throw 'git add mygo.json failed.' }
        & git commit -m "chore(release): $targetTag"
        if ($LASTEXITCODE -ne 0) { throw 'git commit failed.' }
    }

    & git tag -a $targetTag -m "Release $targetTag"
    if ($LASTEXITCODE -ne 0) { throw 'git tag failed.' }

    if ($Push) {
        & git push origin master
        if ($LASTEXITCODE -ne 0) { throw 'git push origin master failed.' }
        & git push origin $targetTag
        if ($LASTEXITCODE -ne 0) { throw "git push origin $targetTag failed." }
        Write-Output "Pushed master and $targetTag to origin."
    } else {
        Write-Output "Created tag $targetTag locally. Push with:"
        Write-Output "  git push origin master && git push origin $targetTag"
    }
} finally {
    Pop-Location
}
