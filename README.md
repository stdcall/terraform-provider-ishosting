# Terraform Provider for ISHosting

Manage [ISHosting](https://ishosting.com) VPS instances with Terraform.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- Go 1.24+ (only if building from source)
- An ISHosting API token — generate one in the ISHosting control panel

---

## Installation (GitHub Release)

This provider is distributed as a binary via [GitHub Releases](https://github.com/Privata-VPN/ishosting-terraform-provider/releases).

### Step 1 — Download the binary

Pick the archive for your platform from the latest release:

| Platform | File |
|----------|------|
| Linux amd64 | `terraform-provider-ishosting_linux_amd64.tar.gz` |
| Linux arm64 | `terraform-provider-ishosting_linux_arm64.tar.gz` |
| macOS amd64 | `terraform-provider-ishosting_darwin_amd64.tar.gz` |
| macOS arm64 (Apple Silicon) | `terraform-provider-ishosting_darwin_arm64.tar.gz` |
| Windows amd64 | `terraform-provider-ishosting_windows_amd64.zip` |

```bash
# Example: macOS Apple Silicon
VERSION=v0.1.5   # replace with the latest release tag
curl -L https://github.com/Privata-VPN/ishosting-terraform-provider/releases/download/${VERSION}/terraform-provider-ishosting_darwin_arm64.tar.gz \
  | tar xz
```

### Step 2 — Place the binary in the local plugins directory

Terraform looks for local providers in:

```
~/.terraform.d/plugins/<hostname>/<namespace>/<type>/<version>/<os_arch>/
```

```bash
# Set your platform and version
HOSTNAME=github.com
NAMESPACE=privata-vpn
TYPE=ishosting
VERSION=0.1.5          # without the "v" prefix
OS_ARCH=darwin_arm64   # match your platform from the table above

PLUGIN_DIR=~/.terraform.d/plugins/${HOSTNAME}/${NAMESPACE}/${TYPE}/${VERSION}/${OS_ARCH}

mkdir -p "$PLUGIN_DIR"
mv terraform-provider-ishosting "$PLUGIN_DIR/terraform-provider-${TYPE}_v${VERSION}"
chmod +x "$PLUGIN_DIR/terraform-provider-${TYPE}_v${VERSION}"
```

### Step 3 — Configure your Terraform project

```hcl
# versions.tf
terraform {
  required_providers {
    ishosting = {
      source  = "github.com/privata-vpn/ishosting"
      version = "~> 0.1"
    }
  }
}
```

Then run:

```bash
terraform init
```

---

## Alternative: dev_overrides (no versioned directory needed)

If you just want to point Terraform at the binary without the full directory structure, add this to `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "github.com/privata-vpn/ishosting" = "/path/to/ishosting-terraform-provider"
  }
  direct {}
}
```

Place the built binary in that directory and **skip `terraform init`** — Terraform will use the binary directly.

---

## Provider Configuration

```hcl
provider "ishosting" {
  api_token = "your-api-token"   # or set ISHOSTING_API_TOKEN env var
}
```

| Argument | Required | Description |
|----------|----------|-------------|
| `api_token` | yes | ISHosting API token. Can also be set via `ISHOSTING_API_TOKEN`. |
| `base_url` | no | API base URL. Defaults to `https://api.ishosting.com`. |

**Recommended:** use an environment variable instead of hardcoding the token:

```bash
export ISHOSTING_API_TOKEN="your-api-token"
```

---

## Resources & Data Sources

### Resources

| Resource | Description |
|----------|-------------|
| `ishosting_vps` | Provision and manage a VPS instance |
| `ishosting_ssh_key` | Manage SSH keys |

### Data Sources

| Data Source | Description |
|-------------|-------------|
| `ishosting_vps_plans` | List available VPS plans (filter by country) |
| `ishosting_vps_configs` | Get configuration options for a plan, including per-country plan codes |
| `ishosting_vps_ips` | List all IPs assigned to a VPS |

---

## Usage Example

```hcl
terraform {
  required_providers {
    ishosting = {
      source  = "github.com/privata-vpn/ishosting"
      version = "~> 0.1"
    }
  }
}

provider "ishosting" {
  # api_token read from ISHOSTING_API_TOKEN env var
}

# Upload your SSH public key
resource "ishosting_ssh_key" "default" {
  title      = "my-key"
  public_key = file("~/.ssh/id_rsa.pub")
}

# Browse available plans in a country. Each plan code (e.g. "29_1m") is tied to a
# single country and billing period.
data "ishosting_vps_plans" "nl" {
  locations = ["NL"]
}

# Provision a VPS. The plan code determines the country, billing period and base
# hardware, so there is no separate "location" argument.
resource "ishosting_vps" "web" {
  plan = "29_1m"
  name = "web-01"
  tags = ["web", "production"]

  # OS image code (find available codes via the ishosting_vps_configs data source)
  os = "linux/ubuntu24#64"

  # Optional add-ons (codes/categories come from ishosting_vps_configs)
  additions = [
    { category = "ram", code = "2g" },
    { category = "ip", quantity = 1 },
  ]

  ssh_enabled = true
  ssh_keys    = [ishosting_ssh_key.default.id]

  auto_renew = true
}

# Read all IPs
data "ishosting_vps_ips" "web" {
  vps_id = ishosting_vps.web.id
}

output "public_ip" {
  value = ishosting_vps.web.public_ip
}

output "all_ipv4" {
  value = data.ishosting_vps_ips.web.ipv4[*].address
}
```

### Finding plan codes and OS images

Plan codes are country-specific. Use the `ishosting_vps_configs` data source to
discover the plan code for each country and the available OS image codes:

```hcl
data "ishosting_vps_configs" "lite" {
  plan_code = "29_1m"
}

# country code -> plan code
output "plan_codes_by_country" {
  value = { for l in data.ishosting_vps_configs.lite.locations : l.code => l.plan }
}

# available OS image codes (parse the raw config JSON)
output "os_images" {
  value = [for o in jsondecode(data.ishosting_vps_configs.lite.config_json).platforms.additions.fixed.os : o.code]
}
```

---

## Building from Source

```bash
git clone https://github.com/Privata-VPN/ishosting-terraform-provider.git
cd ishosting-terraform-provider
make install   # builds and installs to ~/.terraform.d/plugins/
```

---

## License

MIT
