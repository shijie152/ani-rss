@echo off
setlocal
set "ROOT=%~dp0"
if not defined ANI_RSS_CONFIG set "ANI_RSS_CONFIG=%APPDATA%\ani-rss"
if not defined ANI_RSS_PORT set "ANI_RSS_PORT=7789"
if not defined ANI_RSS_LISTEN set "ANI_RSS_LISTEN=127.0.0.1:%ANI_RSS_PORT%"
if not exist "%ANI_RSS_CONFIG%" mkdir "%ANI_RSS_CONFIG%"
start "ANI-RSS" "%ROOT%ani-rss.exe" --gui --listen "%ANI_RSS_LISTEN%" --ui-dir "%ROOT%ui" --config-dir "%ANI_RSS_CONFIG%"
start "" "http://127.0.0.1:%ANI_RSS_PORT%/"
endlocal
