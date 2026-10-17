param(
    [string]$Destination = (Join-Path $env:LOCALAPPDATA 'MIXED-API'),
    [string]$Version = 'latest',
    [switch]$NoLaunch
)
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
$Destination = [IO.Path]::GetFullPath($Destination)
$api = 'https://api.github.com/repos/NLKASHEI/MIXED-API/releases/'
if ($Version -eq 'latest') { $api += 'latest' }
elseif ($Version -match '^v[0-9]+\.[0-9]+\.[0-9]+$') { $api += "tags/$Version" }
else { throw 'Version must be latest or a tag such as v0.3.0.' }
$release = Invoke-RestMethod -Uri $api -Headers @{ 'User-Agent' = 'MIXED-API-Installer' }
if ($release.draft -or $release.prerelease -or $release.tag_name -notmatch '^v[0-9]+\.[0-9]+\.[0-9]+$') { throw 'Expected an official stable release.' }
$asset = @($release.assets | Where-Object { $_.name -eq 'MIXED-API-Windows-x64.zip' })
if ($asset.Count -ne 1 -or $asset[0].digest -notmatch '^sha256:([a-fA-F0-9]{64})$') { throw 'Official ZIP or its SHA-256 is unavailable.' }
$expectedHash = $asset[0].digest.Substring(7)
$expectedURL = "https://github.com/NLKASHEI/MIXED-API/releases/download/$($release.tag_name)/MIXED-API-Windows-x64.zip"
if ($asset[0].browser_download_url -cne $expectedURL) { throw 'Unexpected download source.' }
$exe = Join-Path $Destination 'MIXED-API.exe'
$running = @(Get-Process -Name 'MIXED-API' -ErrorAction SilentlyContinue | Where-Object { $_.Path -eq $exe })
if ($running.Count -gt 0) { throw 'MIXED-API is running from this destination. Close it in Task Manager and retry; existing files have not been changed.' }
$temporaryRoot = [IO.Path]::GetFullPath([IO.Path]::GetTempPath())
$staging = Join-Path $temporaryRoot ('mixed-api-install-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $staging | Out-Null
try {
    $archive = Join-Path $staging 'release.zip'
    Write-Host "Downloading $($release.tag_name)..."
    Invoke-WebRequest -Uri $expectedURL -OutFile $archive -UseBasicParsing
    if ((Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash -ine $expectedHash) { throw 'SHA-256 mismatch; refusing to install.' }
    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $zip = [IO.Compression.ZipFile]::OpenRead($archive)
    try {
        $allowed = @('MIXED-API.exe','Start-MIXED-API.cmd','Start-MIXED-API-LAN.cmd','README.md','Install-MIXED-API.ps1','THIRD_PARTY_NOTICES.md')
        if (@($zip.Entries | Where-Object { $_.FullName -eq 'MIXED-API.exe' }).Count -ne 1) { throw 'Release ZIP has no unique executable.' }
        foreach ($entry in $zip.Entries) { if ($allowed -cnotcontains $entry.FullName) { throw "Unexpected archive entry: $($entry.FullName)" } }
        $files = Join-Path $staging 'files'
        [IO.Compression.ZipFile]::ExtractToDirectory($archive, $files)
    } finally { $zip.Dispose() }
    New-Item -ItemType Directory -Path $Destination -Force | Out-Null
    $config = Join-Path $Destination 'mixed-api.json'
    if (Test-Path -LiteralPath $config) {
        $backup = Join-Path $Destination ('mixed-api.backup-' + (Get-Date -Format 'yyyyMMdd-HHmmss') + '.json')
        Copy-Item -LiteralPath $config -Destination $backup
        Write-Host "Configuration backup: $backup"
    }
    foreach ($name in $allowed) {
        $source = Join-Path $files $name
        if (Test-Path -LiteralPath $source) { Copy-Item -LiteralPath $source -Destination (Join-Path $Destination $name) -Force }
    }
    Write-Host "Installed $($release.tag_name) at $Destination"
    if (!$NoLaunch) { Start-Process -FilePath $exe -WorkingDirectory $Destination -WindowStyle Normal }
} finally {
    $resolvedStaging = [IO.Path]::GetFullPath($staging)
    if (!$resolvedStaging.StartsWith($temporaryRoot, [StringComparison]::OrdinalIgnoreCase) -or (Split-Path $resolvedStaging -Leaf) -notlike 'mixed-api-install-*') { throw 'Refusing to clean an unexpected temporary path.' }
    Remove-Item -LiteralPath $resolvedStaging -Recurse -Force
}
