$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$startup = [Environment]::GetFolderPath('Startup')
$shortcutPath = Join-Path $startup 'ani-rss.lnk'
$shell = New-Object -ComObject WScript.Shell
$shortcut = $shell.CreateShortcut($shortcutPath)
$shortcut.TargetPath = Join-Path $root 'start-ani-rss.cmd'
$shortcut.WorkingDirectory = $root
$shortcut.Description = 'ANI-RSS'
$shortcut.Save()
Write-Host "Created $shortcutPath"
