param(
    [switch]$Race,
    [string]$LogDir = "logs"
)

$ErrorActionPreference = "Stop"

if (!(Test-Path $LogDir)) {
    New-Item -ItemType Directory -Path $LogDir | Out-Null
}

$timestamp = Get-Date -Format "yyyy-MM-ddTHH-mm-ss.fff"
$transcript = Join-Path $LogDir "$timestamp`_validate.log"
Start-Transcript -Path $transcript | Out-Null

function Run-Step {
    param(
        [string]$Name,
        [scriptblock]$Command
    )

    $started = Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fff"
    Write-Host "[$started] START $Name"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
    $finished = Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fff"
    Write-Host "[$finished] PASS  $Name"
}

try {
    Run-Step "go vet" {
        go vet ./...
    }

    if ($Race) {
        Run-Step "go test -race" {
            $env:CGO_ENABLED = "1"
            if ($IsWindows) {
                $racePaths = @(
                    "D:\Programs\w64devkit\bin",
                    "D:\Programs\LLVM\bin"
                ) | Where-Object { Test-Path $_ }
                if ($racePaths.Count -gt 0) {
                    $separator = [string][IO.Path]::PathSeparator
                    $env:PATH = ($racePaths -join $separator) + $separator + $env:PATH
                }
            }
            go test -race ./... -count=1
        }
    }

    Write-Host "[$(Get-Date -Format "yyyy-MM-ddTHH:mm:ss.fff")] LOG   $transcript"
}
finally {
    Stop-Transcript | Out-Null
}
