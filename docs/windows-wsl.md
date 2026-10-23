# Windows installation with Docker Desktop and WSL 2

Use Docker Desktop on Windows with Ubuntu running in WSL 2. Gateway runs Linux
containers; execute the bundle commands in Ubuntu's shell. Docker Desktop must
be running. This guide covers a local installation at **http://localhost:8088**.

## Prepare Windows and Ubuntu

Check the current [Docker Desktop Windows prerequisites](https://docs.docker.com/desktop/setup/install/windows-install/#system-requirements),
including supported Windows versions and hardware virtualization. Follow
[Microsoft's WSL installation guide](https://learn.microsoft.com/en-us/windows/wsl/install).
For a new WSL installation, run in an Administrator PowerShell terminal:

```powershell
wsl --install -d Ubuntu
wsl --update
wsl --list --verbose
```

Restart Windows if requested, launch Ubuntu and complete its Linux user setup.
Confirm Ubuntu shows **VERSION 2** in the listing. For an existing WSL 1
installation, follow Microsoft's upgrade instructions before continuing.

Install [Docker Desktop for Windows](https://docs.docker.com/desktop/setup/install/windows-install/)
and start it. Select Linux containers and the **Use WSL 2 based engine** option
under **Settings → General**, when that option is shown. Under
**Settings → Resources → WSL Integration**, enable Ubuntu and apply the changes.
See [Docker's WSL integration guide](https://docs.docker.com/desktop/features/wsl/).

Use Desktop's integrated Docker engine. Do not also install a separate Docker
Engine or Docker CLI inside Ubuntu for this path. If one already exists, review
Docker's integration guidance and preserve its data before changing the setup.

Open **Ubuntu**, rather than PowerShell, for the remaining commands. Install
Python and download tools there if missing:

```sh
sudo apt update
sudo apt install -y python3 curl ca-certificates

docker version
docker info
docker compose version
python3 --version
```

`docker version` must show a reachable **Server**, and `docker info` must succeed.
Gateway requires Compose **2.24.4+** and Python **3.9+**. If Docker is unavailable,
start Desktop and recheck Ubuntu integration before trying to start Gateway.

## Install a release bundle in Ubuntu

Keep the installation directory in your Ubuntu home directory. Download the
archive and checksum for the same version from
[GitHub Releases](https://github.com/Sociaro-Research/open-media-gateway/releases)
into that directory. Replace `vX.Y.Z` below with the chosen version:

```sh
mkdir -p ~/media-gateway-install
cd ~/media-gateway-install
# Place media-gateway-vX.Y.Z.tar.gz and its .sha256 file here before continuing.
sha256sum -c media-gateway-vX.Y.Z.tar.gz.sha256
tar -xzf media-gateway-vX.Y.Z.tar.gz
cd media-gateway-vX.Y.Z
python3 initialize.py
docker compose pull
docker compose up -d --wait
docker compose exec -T gateway /gateway admin setup-code
```

Stop if checksum verification fails. Follow the extracted bundle's README for
that version's owner setup: open **http://localhost:8088** in your Windows browser
and enter the private setup code. No source build is required. Keep the generated
`.env`, installation directory and named volumes for later starts and updates.

## Start again and diagnose interruptions

Quitting Docker Desktop, suspending/shutting down Windows, or explicitly stopping
WSL can interrupt the required runtime. See Microsoft's
[WSL shutdown command](https://learn.microsoft.com/en-us/windows/wsl/basic-commands#shutdown).
Closing a terminal is not a Gateway stop command. The Compose
[`restart: unless-stopped`](https://docs.docker.com/engine/containers/start-containers-automatically/)
cannot keep a stopped Windows/WSL runtime running;
it applies to containers when their Docker daemon is available.

After an interruption, start Docker Desktop, open Ubuntu, and return to the
**same extracted installation directory**:

```sh
docker info
docker compose up -d --wait
docker compose ps
curl --fail http://localhost:8088/healthz
```

Do not reinitialize an existing installation to restart it. Do not use
`docker compose down -v`, delete volumes, or reset Docker Desktop as a recovery
step: those actions can remove application data.

| Symptom | Check |
| --- | --- |
| `docker` missing or daemon unreachable | Desktop running; Linux containers; WSL 2 engine; Ubuntu integration enabled. |
| Containers stopped after a runtime interruption | Restore Desktop/WSL, then use the commands above; a restart policy is not a host supervisor. |
| Containers unhealthy or browser cannot connect | Check `docker compose ps` and `docker compose logs --tail=100 gateway ui worker postgres redis`; verify `/healthz` from Ubuntu. |

An interruption alone does not identify its cause. Record Windows/WSL/Docker
versions, whether Desktop was running, container state and relevant logs before
changing configuration. For unattended availability, use a host whose runtime
remains available while the service is needed.
