# 🛡️ Proxmox Guardian

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

**Graceful shutdown orchestrator for Proxmox VE with UPS integration**

Proxmox Guardian monitors your UPS via NUT (Network UPS Tools) and orchestrates a clean, ordered shutdown of your entire infrastructure when power fails - VMs, LXC containers, Docker stacks, databases, and services.

## ✨ Features

- 🔋 **NUT Integration** - Monitors UPS battery level and status in real-time
- 📋 **Declarative YAML Config** - Define your shutdown strategy without code
- 🔄 **Phased Shutdown** - Ordered phases with dependencies and priorities
- 🐳 **Docker Support** - Graceful compose down via SSH or pct exec
- 🗄️ **Database Safe** - PostgreSQL, MySQL, Redis shutdown best practices
- ⚡ **Recovery Mode** - Auto-restart services if power returns mid-shutdown
- 🔔 **Notifications** - Webhook alerts (Discord, Slack, etc.)
- 🛡️ **Robust** - Retry logic, healthchecks, graceful degradation

## 🚀 Quick Start

### Installation

```bash
# Download latest release
curl -LO https://github.com/Guilhem-Bonnet/proxmox-guardian/releases/latest/download/proxmox-guardian-linux-amd64
chmod +x proxmox-guardian-linux-amd64
sudo mv proxmox-guardian-linux-amd64 /usr/local/bin/proxmox-guardian
```

### Configuration

```bash
# Create config directory
sudo mkdir -p /etc/proxmox-guardian

# Copy and edit example config
sudo cp configs/guardian.yaml.example /etc/proxmox-guardian/guardian.yaml
sudo chmod 600 /etc/proxmox-guardian/guardian.yaml
sudo vim /etc/proxmox-guardian/guardian.yaml
```

### Commands

```bash
# Validate configuration syntax and connectivity
proxmox-guardian validate

# Show execution plan (dry-run)
proxmox-guardian plan

# Execute shutdown sequence
proxmox-guardian apply

# Start daemon mode (monitors UPS continuously)
proxmox-guardian daemon

# Test commands (validate your setup before relying on it)
proxmox-guardian test connection              # Test NUT, Proxmox API, SSH
proxmox-guardian test shutdown --dry-run      # Simulate full sequence
proxmox-guardian test shutdown --phase=2      # Test specific phase
proxmox-guardian test shutdown --phase=1 --action=1  # Test single action
proxmox-guardian test recovery                # Test recovery sequence
```

## 📝 Configuration Example

```yaml
ups:
  driver: nut
  host: localhost:3493
  name: eaton-ups
  thresholds:
    warning: 30        # Notify at 30%
    critical: 20       # Start shutdown at 20%
    emergency: 10      # Force immediate shutdown

proxmox:
  api_url: https://192.168.1.10:8006/api2/json
  token_id: guardian@pve!shutdown
  secrets_file: /etc/proxmox-guardian/secrets.yaml

phases:
  - name: "stop-applications"
    parallel: true
    actions:
      - type: proxmox-exec
        guest: "lxc:media-stack"
        command: "docker compose -f /opt/stacks/compose.yml down --timeout 60"
        timeout: 120s
        on_error: continue
        retry:
          attempts: 2
          delay: 5s

  - name: "stop-databases"
    actions:
      - type: ssh
        host: "db-server.local"
        user: postgres
        command: "pg_ctl stop -m fast -D /var/lib/postgresql/data"
        healthcheck:
          command: "pg_isready -q"
          expect: failure
        timeout: 60s

  - name: "shutdown-guests"
    actions:
      - type: proxmox-guest
        selector:
          type: lxc
          tags: [non-critical]
        action: shutdown
        timeout: 60s

      - type: proxmox-guest
        selector:
          type: vm
          exclude_tags: [always-on]
        action: shutdown
        timeout: 180s

recovery:
  enabled: true
  power_stable_delay: 60s
  on_error: notify

notifications:
  - type: webhook
    url_env: DISCORD_WEBHOOK_URL
    events: [power_lost, shutdown_start, shutdown_complete, recovery_start]
```

## 🏗️ Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                     Proxmox Guardian                         │
├─────────────────────────────────────────────────────────────┤
│  ┌─────────┐  ┌──────────────┐  ┌─────────────────────────┐ │
│  │   UPS   │  │ Orchestrator │  │       Executors         │ │
│  │ Monitor │─▶│   (Phases)   │─▶│ SSH | Proxmox | Local   │ │
│  └─────────┘  └──────────────┘  └─────────────────────────┘ │
│       │              │                      │               │
│       ▼              ▼                      ▼               │
│  ┌─────────┐  ┌──────────────┐  ┌─────────────────────────┐ │
│  │   NUT   │  │  State File  │  │   Proxmox API / SSH     │ │
│  │  upsd   │  │    (JSON)    │  │   VMs, LXC, Docker      │ │
│  └─────────┘  └──────────────┘  └─────────────────────────┘ │
└─────────────────────────────────────────────────────────────┘
```

## 📦 Project Structure

```
proxmox-guardian/
├── cmd/
│   └── proxmox-guardian/
│       └── main.go              # CLI entry point
├── internal/
│   ├── config/                  # YAML config parsing
│   ├── ups/                     # NUT client
│   ├── executor/                # Action executors
│   │   ├── executor.go          # Interface
│   │   ├── ssh.go
│   │   ├── proxmox_exec.go
│   │   ├── proxmox_guest.go
│   │   └── local.go
│   ├── orchestrator/            # Phase execution engine
│   ├── state/                   # Persistence & recovery
│   ├── proxmox/                 # go-proxmox wrapper
│   └── notifier/                # Webhooks
├── configs/
│   └── guardian.yaml.example
├── systemd/
│   └── proxmox-guardian.service
├── Makefile
├── go.mod
└── README.md
```

## 🔧 NUT Integration

Configure NUT to call Guardian on power events:

```ini
# /etc/nut/upsmon.conf
NOTIFYCMD /usr/local/bin/proxmox-guardian notify
NOTIFYFLAG ONBATT EXEC
NOTIFYFLAG LOWBATT EXEC
NOTIFYFLAG ONLINE EXEC
```

## 🛡️ Security

- **Secrets file** - API tokens stored separately with 0600 permissions
- **Dedicated user** - Run as `guardian` user with minimal sudo rights
- **SSH keys** - Dedicated key per host with restricted commands
- **Lock file** - Prevents concurrent executions

## 📊 Executor Types

| Type | Description | Use Case |
|------|-------------|----------|
| `ssh` | Execute command via SSH | Remote servers, databases |
| `proxmox-exec` | Execute in guest via qm/pct exec | Docker in LXC, services |
| `proxmox-guest` | Shutdown VM/LXC via API | Clean guest shutdown |
| `local` | Execute on Guardian host | Host shutdown, scripts |

## 🤝 Contributing

Contributions welcome! Please read [CONTRIBUTING.md](CONTRIBUTING.md) first.

## 📄 License

MIT License - see [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- [luthermonson/go-proxmox](https://github.com/luthermonson/go-proxmox) - Proxmox API client
- [NUT Project](https://networkupstools.org/) - Network UPS Tools
- Inspired by [jordanmack/proxmox-ups-shutdown](https://github.com/jordanmack/proxmox-ups-shutdown)
