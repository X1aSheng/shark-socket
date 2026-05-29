param(
    [switch]$Race
)

$ErrorActionPreference = "Stop"

function Run-Step {
    param(
        [string]$Name,
        [scriptblock]$Command
    )

    $started = Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fff"
    Write-Host "[$started] START $Name"
    & $Command
    $finished = Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fff"
    Write-Host "[$finished] PASS  $Name"
}

Run-Step "go test" {
    go test ./... -count=1
}

Run-Step "go vet" {
    go vet ./...
}

if ($Race) {
    Run-Step "go test -race" {
        $env:PATH = "D:\Programs\w64devkit\bin;D:\Programs\LLVM\bin;" + $env:PATH
        $env:CGO_ENABLED = "1"
        go test -race ./... -count=1
    }
}
