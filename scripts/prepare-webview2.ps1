param([string]$Cache = (Join-Path $PSScriptRoot '../.build/webview2'))
$ErrorActionPreference = 'Stop'
$RepoRoot = Split-Path $PSScriptRoot -Parent
$Pin = Get-Content -LiteralPath (Join-Path $RepoRoot 'packaging/windows/webview2.json') -Raw | ConvertFrom-Json
$Cache = [IO.Path]::GetFullPath($Cache)
New-Item -ItemType Directory -Path $Cache -Force | Out-Null
$Cab = Join-Path $Cache 'webview2.cab'
if (!(Test-Path -LiteralPath $Cab)) { Invoke-WebRequest -Uri $Pin.url -OutFile $Cab }
if ((Get-FileHash -LiteralPath $Cab -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Pin.sha256) { throw 'Pinned WebView2 CAB checksum mismatch' }
$Expanded = Join-Path $Cache 'expanded'
$RuntimeName = "Microsoft.WebView2.FixedVersionRuntime.$($Pin.version).$($Pin.architecture)"
$Runtime = Join-Path $Expanded $RuntimeName
$Exe = Join-Path $Runtime 'msedgewebview2.exe'
if (!(Test-Path -LiteralPath $Exe)) {
    New-Item -ItemType Directory -Path $Expanded -Force | Out-Null
    & "$env:SystemRoot/System32/expand.exe" '-F:*' $Cab $Expanded | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'WebView2 extraction failed' }
}
if ((Get-FileHash -LiteralPath $Exe -Algorithm SHA256).Hash.ToLowerInvariant() -ne $Pin.executable_sha256) { throw 'WebView2 executable checksum mismatch' }
$Signature = Get-AuthenticodeSignature -LiteralPath $Exe
if ($Signature.Status -ne 'Valid' -or $Signature.SignerCertificate.Subject -notmatch 'O=Microsoft Corporation') { throw 'WebView2 Microsoft signature validation failed' }
if ((Get-Item -LiteralPath $Exe).VersionInfo.FileVersion -ne $Pin.version) { throw 'WebView2 runtime version mismatch' }
$Files = [ordered]@{}
Get-ChildItem -LiteralPath $Runtime -File -Recurse | Sort-Object FullName | ForEach-Object {
    $Relative = [IO.Path]::GetRelativePath($Runtime, $_.FullName).Replace('\','/')
    $Files[$Relative] = (Get-FileHash -LiteralPath $_.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
}
$Resources = Join-Path $RepoRoot 'internal/desktop/resources'
New-Item -ItemType Directory -Path $Resources -Force | Out-Null
Copy-Item -LiteralPath $Cab -Destination (Join-Path $Resources 'webview2.cab') -Force
$Manifest = [ordered]@{ version=$Pin.version; root=$RuntimeName; sha256=$Pin.sha256; files=$Files }
$Manifest | ConvertTo-Json -Depth 4 | Set-Content -LiteralPath (Join-Path $Resources 'manifest.json') -Encoding utf8
Write-Output "Pinned offline renderer prepared: $($Pin.version), $($Files.Count) verified files"
