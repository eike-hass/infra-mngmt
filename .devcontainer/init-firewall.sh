#!/bin/bash
set -euo pipefail  # Exit on error, undefined vars, and pipeline failures
IFS=$'\n\t'       # Stricter word splitting

# 1. Extract Docker DNS info BEFORE any flushing
DOCKER_DNS_RULES=$(iptables-save -t nat | grep "127\.0\.0\.11" || true)

# Flush existing rules and delete existing ipsets
iptables -F
iptables -X
iptables -t nat -F
iptables -t nat -X
iptables -t mangle -F
iptables -t mangle -X
ipset destroy allowed-domains 2>/dev/null || true

# 2. Selectively restore ONLY internal Docker DNS resolution
if [ -n "$DOCKER_DNS_RULES" ]; then
    echo "Restoring Docker DNS rules..."
    iptables -t nat -N DOCKER_OUTPUT 2>/dev/null || true
    iptables -t nat -N DOCKER_POSTROUTING 2>/dev/null || true
    echo "$DOCKER_DNS_RULES" | xargs -L 1 iptables -t nat
else
    echo "No Docker DNS rules to restore"
fi

# First allow DNS and localhost before any restrictions
# Allow outbound DNS
iptables -A OUTPUT -p udp --dport 53 -j ACCEPT
# Allow inbound DNS responses
iptables -A INPUT -p udp --sport 53 -j ACCEPT
# Allow outbound SSH
iptables -A OUTPUT -p tcp --dport 22 -j ACCEPT
# Allow inbound SSH responses
iptables -A INPUT -p tcp --sport 22 -m state --state ESTABLISHED -j ACCEPT
# Allow localhost
iptables -A INPUT -i lo -j ACCEPT
iptables -A OUTPUT -o lo -j ACCEPT

# Create ipset with CIDR support
ipset create allowed-domains hash:net

# Fetch GitHub meta information and aggregate + add their IP ranges
echo "Fetching GitHub IP ranges..."
gh_ranges=$(curl -s https://api.github.com/meta)
if [ -z "$gh_ranges" ]; then
    echo "ERROR: Failed to fetch GitHub IP ranges"
    exit 1
fi

if ! echo "$gh_ranges" | jq -e '.web and .api and .git' >/dev/null; then
    echo "ERROR: GitHub API response missing required fields"
    exit 1
fi

echo "Processing GitHub IPs..."
while read -r cidr; do
    if [[ ! "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
        echo "ERROR: Invalid CIDR range from GitHub meta: $cidr"
        exit 1
    fi
    echo "Adding GitHub range $cidr"
    ipset add allowed-domains "$cidr"
done < <(echo "$gh_ranges" | jq -r '(.web + .api + .git)[]' | aggregate -q)

# Fetch Google IP ranges. Google publishes goog.json daily with every CIDR
# their CDN serves from; using it instead of per-hostname A-record resolution
# avoids the stale-allowlist problem the per-hostname loop caused for
# proxy.golang.org, sum.golang.org, dl.google.com, storage.googleapis.com,
# go.googlesource.com, golang.org, go.dev, pkg.go.dev. Google rotates the
# IPs those names point at faster than the container is rebuilt; the CIDR
# list changes ~weekly and covers every IP we could land on.
echo "Fetching Google IP ranges..."
goog_ranges=$(curl -s https://www.gstatic.com/ipranges/goog.json)
if [ -z "$goog_ranges" ]; then
    echo "ERROR: Failed to fetch Google IP ranges"
    exit 1
fi

if ! echo "$goog_ranges" | jq -e '.prefixes' >/dev/null; then
    echo "ERROR: Google ipranges response missing .prefixes"
    exit 1
fi

echo "Processing Google IPs..."
while read -r cidr; do
    if [[ -z "$cidr" ]]; then continue; fi
    if [[ ! "$cidr" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}/[0-9]{1,2}$ ]]; then
        echo "ERROR: Invalid CIDR range from Google ipranges: $cidr"
        exit 1
    fi
    ipset add allowed-domains "$cidr"
done < <(echo "$goog_ranges" | jq -r '.prefixes[].ipv4Prefix // empty' | aggregate -q)

# Resolve and add other allowed domains. Anything Google-served is handled by
# the goog.json block above — don't add it here, or you'll re-introduce the
# stale-A-record problem.
for domain in \
    "registry.npmjs.org" \
    "api.anthropic.com" \
    "marketplace.visualstudio.com" \
    "vscode.blob.core.windows.net" \
    "update.code.visualstudio.com" \
    "w3.org" \
    "gopkg.in"; do
    echo "Resolving $domain..."
    ips=$(dig +noall +answer A "$domain" | awk '$4 == "A" {print $5}')
    if [ -z "$ips" ]; then
        echo "ERROR: Failed to resolve $domain"
        exit 1
    fi
    
    while read -r ip; do
        if [[ ! "$ip" =~ ^[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}$ ]]; then
            echo "ERROR: Invalid IP from DNS for $domain: $ip"
            exit 1
        fi
        echo "Adding $ip for $domain"
        ipset add allowed-domains "$ip"
    done < <(echo "$ips")
done

# Get host IP from default route
HOST_IP=$(ip route | grep default | cut -d" " -f3)
if [ -z "$HOST_IP" ]; then
    echo "ERROR: Failed to detect host IP"
    exit 1
fi

# Repoint host.docker.internal at the REAL gateway. docker-compose.yml maps this
# name via `extra_hosts: host-gateway`, but Docker hardwires `host-gateway` to
# the *default-bridge* gateway (172.17.0.1) regardless of the container's actual
# network. A Compose container lives on its own bridge whose gateway is the
# default route ($HOST_IP, e.g. 172.22.0.1); host services published for the
# container (the ident-browser MCP in .mcp.json → host.docker.internal:3000)
# answer on THAT gateway, not docker0 — so the baked entry is a dead IP and the
# MCP client can't connect. Rewrite it to $HOST_IP on every start so it stays
# correct and drift-proof across network recreation.
# /etc/hosts is a Docker-managed bind mount, so `sed -i` (rename-over-target)
# fails with EBUSY. Write through the existing inode instead: filter to a temp
# file, then truncate-and-copy back with `cat >`.
grep -v 'host\.docker\.internal' /etc/hosts > /tmp/hosts.new
cat /tmp/hosts.new > /etc/hosts
rm -f /tmp/hosts.new
echo "$HOST_IP	host.docker.internal" >> /etc/hosts
echo "Repointed host.docker.internal -> $HOST_IP"

HOST_NETWORK=$(echo "$HOST_IP" | sed "s/\.[0-9]*$/.0\/24/")
echo "Host network detected as: $HOST_NETWORK"

# Set up remaining iptables rules
iptables -A INPUT -s "$HOST_NETWORK" -j ACCEPT
iptables -A OUTPUT -d "$HOST_NETWORK" -j ACCEPT

# Set default policies to DROP first
iptables -P INPUT DROP
iptables -P FORWARD DROP
iptables -P OUTPUT DROP

# First allow established connections for already approved traffic
iptables -A INPUT -m state --state ESTABLISHED,RELATED -j ACCEPT
iptables -A OUTPUT -m state --state ESTABLISHED,RELATED -j ACCEPT

# Then allow only specific outbound traffic to allowed domains
iptables -A OUTPUT -m set --match-set allowed-domains dst -j ACCEPT

# Explicitly REJECT all other outbound traffic for immediate feedback
iptables -A OUTPUT -j REJECT --reject-with icmp-admin-prohibited

echo "Firewall configuration complete"
echo "Verifying firewall rules..."
if curl --connect-timeout 5 https://example.com >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - was able to reach https://example.com"
    exit 1
else
    echo "Firewall verification passed - unable to reach https://example.com as expected"
fi

# Verify GitHub API access
if ! curl --connect-timeout 5 https://api.github.com/zen >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - unable to reach https://api.github.com"
    exit 1
else
    echo "Firewall verification passed - able to reach https://api.github.com as expected"
fi

# Verify Google module-proxy access. This is the canary for the goog.json
# block — if Google's published CIDR list ever stops covering proxy.golang.org
# (or our fetch of the list silently produced an empty set), Go builds will
# break in subtle ways. Failing here makes that breakage visible at container
# startup instead of mid-build.
if ! curl --connect-timeout 5 -o /dev/null https://proxy.golang.org/ >/dev/null 2>&1; then
    echo "ERROR: Firewall verification failed - unable to reach https://proxy.golang.org"
    exit 1
else
    echo "Firewall verification passed - able to reach https://proxy.golang.org as expected"
fi
