@echo off
setlocal EnableDelayedExpansion

REM ============================================================
REM  shark-socket test runner for Windows
REM
REM  Usage:
REM    scripts\run_tests              -- run all tests
REM    scripts\run_tests --unit       -- unit tests only
REM    scripts\run_tests --integration-- integration tests only
REM    scripts\run_tests --benchmark  -- benchmarks only
REM    scripts\run_tests --cover      -- tests with coverage report
REM    scripts\run_tests --all        -- same as default
REM ============================================================

set "SCRIPT_DIR=%~dp0"
set "PROJECT_DIR=%SCRIPT_DIR%.."
set "LOGDIR=%PROJECT_DIR%\logs"

if not exist "%LOGDIR%" mkdir "%LOGDIR%"

REM --- timestamp: YYYYMMDD_HHMMSS (locale-independent via PowerShell) ---
for /f %%a in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS=%%a"

set "MODE=%1"
if "%MODE%"=="" set "MODE=--all"

REM --- dispatch ---
if "%MODE%"=="--unit"        goto :do_unit
if "%MODE%"=="--integration" goto :do_integration
if "%MODE%"=="--benchmark"   goto :do_benchmark
if "%MODE%"=="--cover"       goto :do_cover
if "%MODE%"=="--all"         goto :do_all
goto :usage

REM ============================================================
REM  :run_and_log  name label [go test args...]
REM ============================================================
:run_and_log
set "RL_NAME=%~1"
set "RL_LABEL=%~2"
shift /1
shift /1

set "JSONFILE=%LOGDIR%\%TS%_%RL_NAME%.json"
set "LOGFILE=%LOGDIR%\%TS%_%RL_NAME%.log"

echo.
echo [92m^>^>^> [%RL_LABEL%] Running...[0m
echo [92m^>^>^> JSON: %JSONFILE%[0m
echo [92m^>^>^> Log:  %LOGFILE%[0m
echo.

REM --- Build go test arg list from remaining params ---
set "GOARGS="
:collect_args
if "%~1"=="" goto :run_go_test
set "GOARGS=%GOARGS% %~1"
shift /1
goto :collect_args

:run_go_test
pushd "%PROJECT_DIR%"
go test%GOARGS% -json -v -count=1 -timeout 300s 2>&1 | tee "%JSONFILE%" || echo. >nul
go run scripts/parse_test_log.go "%JSONFILE%" > "%LOGFILE%" 2>nul || echo. >nul
popd

if exist "%LOGFILE%" (
    echo.
    type "%LOGFILE%"
)
echo.
echo [92m^>^>^> [%RL_LABEL%] Done. Log saved.[0m
echo.
goto :eof

REM ============================================================
:do_unit
call :run_and_log unit "Unit Tests" ./internal/... ./api/... ./tests/unit/...
goto :end

:do_integration
call :run_and_log integration "Integration Tests" ./tests/integration/...
goto :end

:do_benchmark
call :run_and_log benchmark "Benchmarks" -bench=. -benchmem -run=^$ ./tests/benchmark/... ./internal/protocol/tcp/... ./internal/infra/bufferpool/... ./internal/session/... ./internal/plugin/...
goto :end

:do_cover
echo.
echo [93m========================================[0m
echo [93m  Coverage Report[0m
echo [93m  %DATE% %TIME%[0m
echo [93m========================================[0m
echo.
pushd "%PROJECT_DIR%"
go test ./... -count=1 -cover -timeout 300s 2>&1 | tee "%LOGDIR%\%TS%_cover.log"
popd
echo.
echo [92mCoverage log: %LOGDIR%\%TS%_cover.log[0m
goto :end

:do_all
echo.
echo [93m========================================[0m
echo [93m  shark-socket full test suite[0m
echo [93m  %DATE% %TIME%[0m
echo [93m========================================[0m

call :run_and_log unit "Unit Tests" ./internal/... ./api/... ./tests/unit/...
call :run_and_log integration "Integration Tests" ./tests/integration/...
call :run_and_log benchmark "Benchmarks" -bench=. -benchmem -run=^$ ./tests/benchmark/... ./internal/protocol/tcp/... ./internal/infra/bufferpool/... ./internal/session/... ./internal/plugin/...

echo.
echo [92m========================================[0m
echo [92m  All tests complete.[0m
echo [92m  Logs in: %LOGDIR%[0m
dir /b "%LOGDIR%\%TS%_*" 2>nul || echo.  >nul
echo [92m========================================[0m
goto :end

:usage
echo Usage: scripts\run_tests [--unit --integration --benchmark --cover --all]
echo.
echo   --unit         Unit tests only
echo   --integration  Integration tests only
echo   --benchmark    Benchmarks only
echo   --cover        Coverage report
echo   --all          Run all (default)
exit /b 1

:end
endlocal
