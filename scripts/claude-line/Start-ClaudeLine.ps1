# Run as the desktop user. SSH authentication and host verification use Windows OpenSSH.
[CmdletBinding()]
param(
    [Parameter(Mandatory)][ValidatePattern('^[a-zA-Z0-9][a-zA-Z0-9_.@:-]*$')][string]$SshTarget,
    [ValidateRange(1,65535)][int]$SshPort = 22,
    [ValidateRange(1024,65535)][int]$LocalPort = 10880
)
$ErrorActionPreference = 'Stop'
$identity = [Security.Principal.WindowsIdentity]::GetCurrent()
if ((New-Object Security.Principal.WindowsPrincipal($identity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this launcher from a non-elevated PowerShell.'
}
$pkg = Get-AppxPackage -Name Claude | Select-Object -First 1
if (!$pkg) { throw 'Claude MSIX package not found.' }
$manifest = Get-AppxPackageManifest $pkg
$apps = @($manifest.Package.Applications.Application | Where-Object { $_.Executable -match '(^|[\\/])claude\.exe$' })
if ($apps.Count -ne 1) { throw 'Could not identify a unique Claude executable.' }
$exe = Join-Path $pkg.InstallLocation $apps[0].Executable
if (Get-Process -Name Claude -ErrorAction SilentlyContinue) { throw 'Exit Claude from the tray first.' }
$rule = Get-NetFirewallRule -PolicyStore ActiveStore -Name ('Chuanyun-ClaudeLine-' + $pkg.PackageFamilyName) -ErrorAction SilentlyContinue
if (!$rule -or $rule.Enabled -ne 'True' -or $rule.Action -ne 'Block') { throw 'Install the package guard first.' }
if (Get-NetFirewallProfile -PolicyStore ActiveStore | Where-Object { !$_.Enabled }) { throw 'Windows Firewall must be enabled for all profiles.' }
$ssh = Join-Path $env:SystemRoot 'System32\OpenSSH\ssh.exe'
$proc = Start-Process -PassThru -NoNewWindow -FilePath $ssh -ArgumentList @('-N','-o','ExitOnForwardFailure=yes','-o','ServerAliveInterval=15','-o','ServerAliveCountMax=2','-D',"127.0.0.1:$LocalPort",'-p',$SshPort,$SshTarget)
try {
    $limit = (Get-Date).AddSeconds(90)
    do {
        if ($proc.HasExited) { throw 'SSH exited before opening the proxy.' }
        $ready = Get-NetTCPConnection -State Listen -LocalAddress 127.0.0.1 -LocalPort $LocalPort -ErrorAction SilentlyContinue | Where-Object OwningProcess -eq $proc.Id
        if (!$ready) { Start-Sleep -Milliseconds 300 }
    } until ($ready -or (Get-Date) -gt $limit)
    if (!$ready) { throw 'Timed out waiting for SSH.' }
    Start-Process -FilePath $exe -ArgumentList "--proxy-server=socks5://127.0.0.1:$LocalPort",'--disable-quic','"--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE localhost"'
    Write-Host 'Dedicated SSH proxy active. Ctrl+C closes the tunnel.'
    $proc.WaitForExit()
} finally {
    if (!$proc.HasExited) { $proc.Kill() }
}
