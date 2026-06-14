param(
    [string]$LogDir = "logs"
)

$ErrorActionPreference = "Stop"

if (!(Test-Path $LogDir)) {
    New-Item -ItemType Directory -Path $LogDir | Out-Null
}

$timestamp = Get-Date -Format "yyyy-MM-ddTHH-mm-ss"
$transcript = Join-Path $LogDir "$timestamp`_deploy.log"
Start-Transcript -Path $transcript | Out-Null

function Run-Step {
    param(
        [string]$Name,
        [scriptblock]$Command
    )

    $started = Get-Date -Format "yyyy-MM-ddTHH:mm:ss"
    Write-Host "[$started] START $Name"
    & $Command
    if ($LASTEXITCODE -ne 0) {
        throw "$Name failed with exit code $LASTEXITCODE"
    }
    $finished = Get-Date -Format "yyyy-MM-ddTHH:mm:ss"
    Write-Host "[$finished] PASS  $Name"
}

function Run-Optional {
    param(
        [string]$Name,
        [string]$Tool,
        [scriptblock]$Command
    )

    if (Get-Command $Tool -ErrorAction SilentlyContinue) {
        Run-Step $Name $Command
    }
    else {
        $now = Get-Date -Format "yyyy-MM-ddTHH:mm:ss"
        Write-Host "[$now] SKIP  $Name ($Tool not found)"
    }
}

try {
    Run-Step "deploy static tests" {
        go test ./tests/deploy -count=1 -v
    }

    Run-Optional "docker compose config" "docker" {
        docker compose -f deploy/docker/docker-compose.yml config
    }

    Run-Optional "kubectl kustomize" "kubectl" {
        kubectl kustomize deploy/k8s
    }

    Run-Optional "helm template" "helm" {
        helm template shark-socket deploy/helm/shark-socket
    }

    $now = Get-Date -Format "yyyy-MM-ddTHH:mm:ss"
    Write-Host "[$now] LOG   $transcript"
}
finally {
    Stop-Transcript | Out-Null
}
