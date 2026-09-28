# Install (or upgrade) probe-agent as a Windows service.
#
# Windows 10 / 11 and Windows Server 2016 or newer, x64 / ARM64 / 32-bit x86.
# Nothing else to install: the agent is one statically linked executable
# (no .NET, Visual C++ runtime or Npcap needed) and this script only uses the
# PowerShell 5.1 that ships with Windows.
#
# Every node has its own token: create the node on the dashboard (Agents page)
# and copy its PowerShell command. From a PowerShell window started as
# Administrator:
#
#   $env:PROBE_SERVER='https://probe.example.com'
#   $env:PROBE_TOKEN='<this node token from the dashboard>'
#   $env:PROBE_NAME='home-win'
#   $env:PROBE_LOCATION='Guangdong Shenzhen'
#   $env:PROBE_ISP='China Telecom'
#   irm "$env:PROBE_SERVER/install-agent.ps1" | iex
#
# The token is the node's identity: re-running with the same token overwrites
# the install in place (upgrade, repair, new PC, new labels) and the dashboard
# keeps the same node, history and monitors.
#
# Uninstall (service, firewall rules and files; the node and its token stay on
# the dashboard until you delete the node there):
#
#   $env:PROBE_UNINSTALL='1'; irm "$env:PROBE_SERVER/install-agent.ps1" | iex
#
# Layout: C:\ProgramData\probe-agent\probe-agent.exe (the service; it updates
# itself when the dashboard is upgraded), probe-agent.env (configuration,
# readable by SYSTEM and Administrators only) and probe-agent.log (5 MB,
# rotated once). A node migrated off the old shared token may also have
# probe-agent.token (its own token); this script folds it into the config.
#
# Kept ASCII-only on purpose: PowerShell 5.1 reads a script file without a
# BOM as ANSI and would garble anything else.

# Get-IssuedToken returns the node token kept in probe-agent.token when it was
# handed over to replace $Configured (the old shared token), else $null.
function Get-IssuedToken {
    param([string]$TokenFile, [string]$Configured)
    if (-not $Configured -or -not (Test-Path $TokenFile)) { return $null }
    $kv = @{}
    foreach ($line in (Get-Content -Path $TokenFile -ErrorAction SilentlyContinue)) {
        if ($line -match '^(PROBE_TOKEN|REPLACES_SHA256)=(.*)$') { $kv[$Matches[1]] = $Matches[2].Trim() }
    }
    $sha = [Security.Cryptography.SHA256]::Create()
    $hash = -join ($sha.ComputeHash([Text.Encoding]::UTF8.GetBytes($Configured)) | ForEach-Object { $_.ToString('x2') })
    if ($kv['PROBE_TOKEN'] -and $kv['REPLACES_SHA256'] -eq $hash) { return $kv['PROBE_TOKEN'] }
    return $null
}

function Install-ProbeAgent {
    $ErrorActionPreference = 'Stop'
    $ProgressPreference = 'SilentlyContinue'
    # Older .NET builds default Invoke-WebRequest to TLS 1.0, which no dashboard behind Caddy / nginx accepts.
    try { [Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12 } catch { }

    $svc = 'probe-agent'
    $dir = Join-Path $env:ProgramData 'probe-agent'
    $exe = Join-Path $dir 'probe-agent.exe'
    $envFile = Join-Path $dir 'probe-agent.env'
    $logFile = Join-Path $dir 'probe-agent.log'
    $tokenFile = Join-Path $dir 'probe-agent.token'

    $principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
    if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
        throw 'run this from a PowerShell window started as Administrator (right-click Start -> Terminal (Admin) / Windows PowerShell (Admin))'
    }
    if ([Environment]::OSVersion.Version.Major -lt 10) { throw 'probe-agent needs Windows 10 / Windows Server 2016 or newer' }
    if ($PSVersionTable.PSVersion.Major -lt 5) { throw 'PowerShell 5.1 or newer is required (it ships with Windows 10)' }

    if ($env:PROBE_UNINSTALL) { Uninstall-ProbeAgent -Svc $svc -Dir $dir -Exe $exe; return }

    if (-not $env:PROBE_SERVER) { throw "PROBE_SERVER is required, e.g. `$env:PROBE_SERVER='https://probe.example.com'" }
    if (-not $env:PROBE_TOKEN) { throw 'PROBE_TOKEN is required: copy the PowerShell install command of this node from the dashboard Agents page' }
    $token = $env:PROBE_TOKEN.Trim()
    # Re-run with the old shared token on a node that has since been handed its own token: keep the node token.
    $own = Get-IssuedToken -TokenFile $tokenFile -Configured $token
    if ($own) { Write-Host 'this node already has its own token; using it instead of the shared one'; $token = $own }
    $server = $env:PROBE_SERVER.Trim().TrimEnd('/')
    if ($server -notmatch '^[a-z]+://') { $server = "https://$server" }
    $name = if ($env:PROBE_NAME) { $env:PROBE_NAME } else { $env:COMPUTERNAME }

    # PROCESSOR_ARCHITEW6432 is set when PowerShell itself runs emulated (32-bit or x64 on ARM64).
    $arch = if ($env:PROCESSOR_ARCHITEW6432) { $env:PROCESSOR_ARCHITEW6432 } else { $env:PROCESSOR_ARCHITECTURE }
    $key = switch ($arch) {
        'AMD64' { 'windows-amd64' }
        'ARM64' { 'windows-arm64' }
        'x86'   { 'windows-386' }
        default { throw "unsupported CPU architecture: $arch" }
    }

    Write-Host "downloading $key from $server ..."
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $tmp = Join-Path $dir ('probe-agent-download-' + [IO.Path]::GetRandomFileName() + '.exe')
    try {
        Invoke-WebRequest -UseBasicParsing -Uri "$server/api/agent/download/$key" -Headers @{ Authorization = "Bearer $token" } -OutFile $tmp
    } catch {
        throw "download failed: $($_.Exception.Message) -- check PROBE_SERVER, that PROBE_TOKEN is the current token of this node (401 = unknown, reset or deleted node), and that the dashboard ships agent binaries (PROBE_AGENTS_DIR)"
    }
    Unblock-File -Path $tmp -ErrorAction SilentlyContinue
    try {
        $ver = (& $tmp version 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -ne 0) { throw $ver }
    } catch {
        Remove-Item $tmp -Force -ErrorAction SilentlyContinue
        throw "the downloaded binary does not run on this machine: $($_.Exception.Message)"
    }

    # A running executable cannot be replaced: stop the service (and any stray copy) first.
    $existing = Get-Service -Name $svc -ErrorAction SilentlyContinue
    if ($existing -and $existing.Status -ne 'Stopped') {
        Write-Host "stopping $svc ..."
        Stop-Service -Name $svc -Force -ErrorAction SilentlyContinue
        try { $existing.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(30)) } catch { }
    }
    Get-Process -Name 'probe-agent' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe } | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-ChildItem -Path $dir -Filter '.probe-agent-update-*' -Force -ErrorAction SilentlyContinue | Remove-Item -Force -ErrorAction SilentlyContinue
    Remove-Item -Path "$exe.prev" -Force -ErrorAction SilentlyContinue
    $moved = $false
    for ($i = 0; $i -lt 10 -and -not $moved; $i++) {
        try { Move-Item -Path $tmp -Destination $exe -Force; $moved = $true } catch { Start-Sleep -Seconds 1 }
    }
    if (-not $moved) { Remove-Item $tmp -Force -ErrorAction SilentlyContinue; throw "could not replace $exe (still in use?)" }

    # Configuration: UTF-8 without BOM so non-ASCII location / ISP names survive; SYSTEM and Administrators only.
    $lines = @(
        "PROBE_SERVER=$server",
        "PROBE_TOKEN=$token",
        "PROBE_NAME=$name",
        "PROBE_LOCATION=$env:PROBE_LOCATION",
        "PROBE_ISP=$env:PROBE_ISP",
        "PROBE_TAGS=$env:PROBE_TAGS"
    )
    [IO.File]::WriteAllText($envFile, (($lines -join "`r`n") + "`r`n"), (New-Object Text.UTF8Encoding $false))
    & icacls $envFile /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
    # The token just written is authoritative from now on.
    Remove-Item -Path $tokenFile, "$tokenFile.tmp" -Force -ErrorAction SilentlyContinue

    # Windows Defender Firewall: let echo replies / time exceeded / unreachable reach the agent's raw ICMP socket.
    # The agent listens on no TCP/UDP port, so these are the only inbound rules it needs.
    try {
        Get-NetFirewallRule -DisplayName 'probe-agent*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
        New-NetFirewallRule -DisplayName 'probe-agent ICMPv4' -Program $exe -Direction Inbound -Action Allow -Protocol ICMPv4 -Profile Any | Out-Null
        New-NetFirewallRule -DisplayName 'probe-agent ICMPv6' -Program $exe -Direction Inbound -Action Allow -Protocol ICMPv6 -Profile Any | Out-Null
    } catch {
        Write-Host "warning: could not add firewall rules ($($_.Exception.Message)); if ping / mtr show 100% loss, allow ICMP for $exe" -ForegroundColor Yellow
    }

    Write-Host "registering service $svc ..."
    & $exe service install --env-file $envFile
    if ($LASTEXITCODE -ne 0) { throw "service install failed (exit code $LASTEXITCODE); see $logFile" }

    Start-Sleep -Seconds 2
    $status = (Get-Service -Name $svc).Status
    Write-Host ''
    Write-Host "installed $ver as Windows service '$svc' ($status)" -ForegroundColor Green
    Write-Host "  node name : $name   (re-run the same command, same token, to upgrade or reconfigure in place)"
    Write-Host "  config    : $envFile"
    Write-Host "  log       : $logFile"
    Write-Host "  self-test : & '$exe' test www.qq.com"
    Write-Host "  manage    : & '$exe' service status | restart | stop | uninstall   (as Administrator)"
    if ($status -ne 'Running') { Write-Host "the service is not running; check $logFile" -ForegroundColor Yellow }
}

function Uninstall-ProbeAgent {
    param([string]$Svc, [string]$Dir, [string]$Exe)
    $ErrorActionPreference = 'Continue'
    if (Get-Service -Name $Svc -ErrorAction SilentlyContinue) {
        if (Test-Path $Exe) {
            & $Exe service uninstall
        } else {
            Stop-Service -Name $Svc -Force -ErrorAction SilentlyContinue
            & sc.exe delete $Svc | Out-Null
        }
        Write-Host "removed service $Svc"
    } else {
        Write-Host "service $Svc is not installed"
    }
    Get-Process -Name 'probe-agent' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $Exe } | Stop-Process -Force -ErrorAction SilentlyContinue
    Get-NetFirewallRule -DisplayName 'probe-agent*' -ErrorAction SilentlyContinue | Remove-NetFirewallRule -ErrorAction SilentlyContinue
    if (Test-Path $Dir) {
        if ($env:PROBE_KEEP_CONFIG) {
            # Keep the config usable: a migrated node's own token lives in probe-agent.token, which goes away.
            $envFile = Join-Path $Dir 'probe-agent.env'
            if (Test-Path $envFile) {
                $lines = @(Get-Content -Path $envFile)
                $cur = ($lines | Where-Object { $_ -like 'PROBE_TOKEN=*' } | Select-Object -First 1)
                if ($cur) {
                    $own = Get-IssuedToken -TokenFile (Join-Path $Dir 'probe-agent.token') -Configured $cur.Substring('PROBE_TOKEN='.Length).Trim()
                    if ($own) {
                        $lines = $lines | ForEach-Object { if ($_ -like 'PROBE_TOKEN=*') { "PROBE_TOKEN=$own" } else { $_ } }
                        [IO.File]::WriteAllText($envFile, (($lines -join "`r`n") + "`r`n"), (New-Object Text.UTF8Encoding $false))
                        Write-Host "moved the node's own token into $envFile"
                    }
                }
            }
            Get-ChildItem -Path $Dir -Force | Where-Object { $_.Name -ne 'probe-agent.env' } | Remove-Item -Force -Recurse -ErrorAction SilentlyContinue
            Write-Host "removed $Dir except probe-agent.env"
        } else {
            Remove-Item -Path $Dir -Recurse -Force -ErrorAction SilentlyContinue
            Write-Host "removed $Dir"
        }
    }
    Write-Host 'probe-agent uninstalled. The node stays listed (offline) on the dashboard and its token stays valid:' -ForegroundColor Green
    Write-Host 'delete the node on the Agents page to revoke it, or reinstall later with the same command.' -ForegroundColor Green
}

Install-ProbeAgent
