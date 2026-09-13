param(
  [Parameter(Mandatory = $true)][string]$Source,
  [Parameter(Mandatory = $true)][string]$Target,
  [string]$StartScript = ''
)
$ErrorActionPreference = 'Stop'
Start-Sleep -Seconds 2
for ($i = 0; $i -lt 30; $i++) {
  try {
    Move-Item -Force -LiteralPath $Source -Destination $Target
    break
  } catch {
    if ($i -eq 29) { throw }
    Start-Sleep -Milliseconds 500
  }
}
if ($StartScript -ne '') { Start-Process -FilePath $StartScript -WorkingDirectory (Split-Path -Parent $StartScript) }
