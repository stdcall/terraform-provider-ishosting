terraform {
  required_providers {
    ishosting = {
      source = "registry.terraform.io/ishosting/ishosting"
    }
  }
}

# Configure the ISHosting provider.
# Set the ISHOSTING_API_TOKEN environment variable or use the api_token attribute.
provider "ishosting" {
  # api_token = "your-api-token-here"
}

# ─── Data Sources ───────────────────────────────────────────────

# Look up available VPS plans, optionally filtered by ISO country code.
# Each plan code (e.g. "29_1m") is tied to a single country and billing period.
data "ishosting_vps_plans" "nl" {
  locations = ["NL"]
}

# Look up available configs (OS images, RAM/drive options, control panels,
# and the per-country plan codes) for a specific plan.
data "ishosting_vps_configs" "plan_configs" {
  plan_code = "29_1m"
}

# ─── SSH Key ────────────────────────────────────────────────────

# Create an SSH key for VPS access
resource "ishosting_ssh_key" "my_key" {
  title      = "my-terraform-key"
  public_key = file("~/.ssh/id_rsa.pub")
}

# ─── VPS Instance ──────────────────────────────────────────────

# Provision a VPS instance.
# The plan code fully determines the country, period and base hardware, so there
# is no separate "location" argument (it is exposed as a computed attribute).
resource "ishosting_vps" "web_server" {
  plan = "29_1m" # Netherlands, Lite Linux NVMe, 1 month
  name = "web-server-01"
  tags = ["web", "production"]

  # OS image code (see data.ishosting_vps_configs ... platforms.additions.fixed.os).
  os = "linux/ubuntu24#64"

  # Optional add-ons: extra RAM, larger drive, extra IPs, control panel, etc.
  # Codes/categories come from the ishosting_vps_configs data source.
  additions = [
    { category = "ram", code = "2g" }, # upgrade to 2 GB RAM
    { category = "ip", quantity = 1 },  # one extra IPv4
  ]

  # Access settings
  vnc_enabled = false
  ssh_enabled = true
  ssh_keys    = [ishosting_ssh_key.my_key.id]

  auto_renew = true
}

# ─── VPS IPs ───────────────────────────────────────────────────

# Read all IPs assigned to the VPS
data "ishosting_vps_ips" "web_server_ips" {
  vps_id = ishosting_vps.web_server.id
}

# ─── Outputs ───────────────────────────────────────────────────

output "vps_id" {
  value = ishosting_vps.web_server.id
}

output "vps_public_ip" {
  value = ishosting_vps.web_server.public_ip
}

output "vps_status" {
  value = ishosting_vps.web_server.status
}

output "vps_country" {
  value = ishosting_vps.web_server.location
}

output "vps_ipv4_addresses" {
  value = data.ishosting_vps_ips.web_server_ips.ipv4
}

output "vps_ipv6_addresses" {
  value = data.ishosting_vps_ips.web_server_ips.ipv6
}

# Per-country plan codes for the selected plan (country -> plan code)
output "plan_codes_by_country" {
  value = {
    for l in data.ishosting_vps_configs.plan_configs.locations : l.code => l.plan
  }
}

output "available_plans" {
  value = [for p in data.ishosting_vps_plans.nl.plans : {
    code     = p.code
    name     = p.name
    price    = p.price
    country  = p.location_name
    cpu      = p.cpu
    ram      = p.ram
    drive    = p.drive
    os       = p.os
  }]
}
