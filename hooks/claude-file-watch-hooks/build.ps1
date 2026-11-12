# Builds the file-watch hooks and hooks_setup into bin\.
# -trimpath keeps local file paths out of the binaries.
$ErrorActionPreference = "Stop"

$env:Path = [Environment]::GetEnvironmentVariable("Path","User") + ";" + [Environment]::GetEnvironmentVariable("Path","Machine")

$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$goDir = Join-Path $root "go"
$bin = Join-Path $root "bin"

New-Item -ItemType Directory -Force -Path $bin | Out-Null

$targets = @(
    @{ Pkg = "./cmd/file_watch_track"; Out = "file_watch_track.exe" },
    @{ Pkg = "./cmd/file_watch_mark_edit"; Out = "file_watch_mark_edit.exe" },
    @{ Pkg = "./cmd/file_watch_check"; Out = "file_watch_check.exe" },
    @{ Pkg = "./cmd/diff_test_cli"; Out = "diff_test_cli.exe" },
    @{ Pkg = "./cmd/hooks_setup"; Out = "hooks_setup.exe" }
)

Push-Location $goDir
try {
    foreach ($t in $targets) {
        $out = Join-Path $bin $t.Out
        Write-Host "building $($t.Out) ..."
        go build -trimpath -o $out $t.Pkg
        if ($LASTEXITCODE -ne 0) {
            throw "build failed: $($t.Out)"
        }
    }
} finally {
    Pop-Location
}

Write-Host "done: $bin"
