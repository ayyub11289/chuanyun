#Requires -RunAsAdministrator
[CmdletBinding()]
param([switch]$Remove)
$ErrorActionPreference = 'Stop'
$pkg = Get-AppxPackage -Name Claude | Select-Object -First 1
if (!$pkg) { throw 'Claude MSIX package not found for this user.' }
$name = 'Chuanyun-ClaudeLine-' + $pkg.PackageFamilyName
if ($Remove) {
    Remove-NetFirewallRule -Name $name -ErrorAction Stop
    return
}
if (Get-NetFirewallRule -Name $name -ErrorAction SilentlyContinue) { throw 'Guard already exists.' }
# Derive the package SID from the stable package family, independent of the versioned EXE path.
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Security.Principal;
public static class ClaudePackageIdentity {
    [DllImport("userenv.dll", CharSet=CharSet.Unicode)]
    static extern int DeriveAppContainerSidFromAppContainerName(string name, out IntPtr sid);
    [DllImport("advapi32.dll")]
    static extern IntPtr FreeSid(IntPtr sid);
    public static string GetSid(string name) {
        IntPtr sid;
        Marshal.ThrowExceptionForHR(DeriveAppContainerSidFromAppContainerName(name, out sid));
        try { return new SecurityIdentifier(sid).Value; } finally { FreeSid(sid); }
    }
}
'@
$sid = [ClaudePackageIdentity]::GetSid($pkg.PackageFamilyName)
New-NetFirewallRule -Name $name -DisplayName $name -Direction Outbound -Action Block -Profile Any -Package $sid -RemoteAddress @('0.0.0.0-127.0.0.0','127.0.0.2-255.255.255.255','::2-ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff') -PolicyStore PersistentStore
