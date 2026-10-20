#!/usr/bin/env bash
# Sets up a ZeroTier network for an end-to-end test of penguintier-dnsd.
# Run it in a VM or test container (it joins a network and adds a route),
# not on a machine you care about.
#
#   scripts/zt-selftest-network.sh [up]   create (or reuse) the test network and join it
#   scripts/zt-selftest-network.sh status show the network and what resolved has for it
#   scripts/zt-selftest-network.sh down   leave the network and delete it
#
# "up" uses this node's own network controller when it has one: it creates
# a private network "penguintier-selftest" with DNS domain pt.test, gives
# this node the fixed managed IP <subnet>.1, makes that IP the network's DNS
# server, authorizes the node and joins. No ZeroTier account needed.
# Allow DNS stays off, so you can switch it on yourself and watch the daemon.
#
# ZeroTier 1.16+ builds may leave the controller out. Then "up" prints the
# steps to make the same network at my.zerotier.com instead.
#
# Environment:
#   PT_TOKEN_FILE  API token file (default: $PENGUINTIER_TOKEN_FILE, then
#                  ~/.zeroTierOneAuthToken, then `sudo cat` the system token)
#   PT_API         API address (default http://127.0.0.1:<zerotier-one.port or 9993>)
#   PT_SUBNET      first three octets of the test subnet (default 10.199.47)
#
# Exit codes: 0 done, 1 error, 3 no controller (manual steps printed).
#
# Needs curl and jq.
set -euo pipefail

network_name=penguintier-selftest
domain=pt.test
subnet=${PT_SUBNET:-10.199.47}
node_ip="$subnet.1"

die() {
	echo "zt-selftest-network: $*" >&2
	exit 1
}

for tool in curl jq; do
	command -v "$tool" >/dev/null || die "$tool is not installed (Debian/Ubuntu: sudo apt install $tool)"
done

# --- API access -------------------------------------------------------------

if [ -n "${PT_API:-}" ]; then
	api=${PT_API%/}
else
	port=$(cat /var/lib/zerotier-one/zerotier-one.port 2>/dev/null || echo 9993)
	api="http://127.0.0.1:$port"
fi

# The token goes into a header file only we can read, not onto curl's
# command line, where other users could see it in `ps`.
header_file=$(mktemp)
trap 'rm -f "$header_file"' EXIT
chmod 600 "$header_file"

token_file=${PT_TOKEN_FILE:-${PENGUINTIER_TOKEN_FILE:-}}
if [ -z "$token_file" ] && [ -r "$HOME/.zeroTierOneAuthToken" ]; then
	token_file="$HOME/.zeroTierOneAuthToken"
fi
if [ -n "$token_file" ]; then
	token=$(tr -d '[:space:]' <"$token_file")
else
	echo "Reading the ZeroTier API token with sudo..." >&2
	token=$(sudo cat /var/lib/zerotier-one/authtoken.secret | tr -d '[:space:]')
fi
[ -n "$token" ] || die "empty API token"
printf 'X-ZT1-Auth: %s\n' "$token" >"$header_file"
unset token

# api_call METHOD PATH [JSON-BODY]: prints the reply body, fails on HTTP
# errors (with the body on stderr).
api_call() {
	local method=$1 path=$2 body=${3:-}
	local out code
	out=$(mktemp)
	if [ -n "$body" ]; then
		code=$(curl -sS --noproxy '*' -o "$out" -w '%{http_code}' -X "$method" -H @"$header_file" \
			-H 'Content-Type: application/json' --data "$body" "$api$path") || {
			rm -f "$out"
			die "$method $path: cannot reach the ZeroTier service at $api"
		}
	else
		code=$(curl -sS --noproxy '*' -o "$out" -w '%{http_code}' -X "$method" -H @"$header_file" "$api$path") || {
			rm -f "$out"
			die "$method $path: cannot reach the ZeroTier service at $api"
		}
	fi
	if [ "${code:0:1}" != 2 ]; then
		echo "$method $path: HTTP $code: $(head -c 300 "$out")" >&2
		rm -f "$out"
		return 1
	fi
	cat "$out"
	rm -f "$out"
}

# has_controller: true if GET /controller answers {"controller": true}.
has_controller() {
	local reply
	reply=$(api_call GET /controller 2>/dev/null) || return 1
	[ "$(jq -r '.controller // false' <<<"$reply")" = true ]
}

# find_network: prints the ID of our test network on this controller, if any.
find_network() {
	local id
	for id in $(api_call GET /controller/network | jq -r '.[]'); do
		if [ "$(api_call GET "/controller/network/$id" | jq -r .name)" = "$network_name" ]; then
			echo "$id"
			return 0
		fi
	done
	return 1
}

node=$(api_call GET /status | jq -r .address) || die "GET /status failed (is the token right?)"

# --- commands ---------------------------------------------------------------

manual_steps() {
	cat <<EOF
This ZeroTier node has no network controller (ZeroTier 1.16 and later may be
built without it). Make the test network at my.zerotier.com instead (free):

  1. Sign in at https://my.zerotier.com and create a network. Note its
     16-character network ID.
  2. Join it from this machine:
       go run ./cmd/ztctl join <network-id>      (or: sudo zerotier-cli join <network-id>)
  3. In the network's member list, authorize this node: $node
     It gets an IP from the network's managed range (for example 10.147.17.23).
  4. In the network's DNS settings, set the search domain to "$domain" and add
     that IP as the DNS server. Save.
  5. Check that the node sees it (status OK, DNS domain $domain):
       go run ./cmd/ztctl list

Then follow "End-to-end test" under "DNS daemon" in docs/testing.md, using that
IP wherever it says $node_ip.
EOF
}

cmd_up() {
	if ! has_controller; then
		manual_steps
		exit 3
	fi

	local nwid
	if nwid=$(find_network); then
		echo "Reusing network $nwid ($network_name)."
	else
		# "<node>______" (six underscores) asks the controller to pick a
		# free network ID.
		local config
		config=$(jq -n --arg name "$network_name" --arg domain "$domain" --arg subnet "$subnet" '{
			name: $name,
			private: true,
			v4AssignMode: {zt: true},
			ipAssignmentPools: [{ipRangeStart: ($subnet + ".100"), ipRangeEnd: ($subnet + ".200")}],
			routes: [{target: ($subnet + ".0/24"), via: null}],
			dns: {domain: $domain, servers: [($subnet + ".1")]}
		}')
		nwid=$(api_call POST "/controller/network/${node}______" "$config" | jq -r .id)
		[ -n "$nwid" ] && [ "$nwid" != null ] || die "the controller did not return a network ID"
		echo "Created network $nwid ($network_name) on this node's controller."
	fi

	# Authorize ourselves with the fixed IP that is also the DNS server.
	api_call POST "/controller/network/$nwid/member/$node" \
		"$(jq -n --arg ip "$node_ip" '{authorized: true, ipAssignments: [$ip]}')" >/dev/null
	# Join. An empty object keeps the default flags (allowDNS off).
	api_call POST "/network/$nwid" '{}' >/dev/null

	echo "Joined. Waiting for the network to come up..."
	local reply status
	for _ in $(seq 1 30); do
		reply=$(api_call GET "/network/$nwid")
		status=$(jq -r .status <<<"$reply")
		[ "$status" = OK ] && break
		sleep 1
	done
	[ "$status" = OK ] || die "network $nwid is still $status after 30 s"

	local iface
	iface=$(jq -r .portDeviceName <<<"$reply")
	echo
	jq -r '"  network:    \(.id) (\(.name))",
		"  interface:  \(.portDeviceName)",
		"  addresses:  \(.assignedAddresses | join(", "))",
		"  DNS:        domain \(.dns.domain), servers \(.dns.servers | join(", "))",
		"  allowDNS:   \(.allowDNS)"' <<<"$reply"
	cat <<EOF

Next (see "End-to-end test" under "DNS daemon" in docs/testing.md):
  # something that answers DNS for $domain on $node_ip, for example dnsmasq:
  sudo dnsmasq --no-daemon --no-resolv --no-hosts --bind-interfaces \\
       --listen-address=$node_ip --address=/host.$domain/$node_ip &
  go run ./cmd/ztctl set $nwid allowDNS=true
  resolvectl status $iface          # within 5 s: DNS server $node_ip, domain ~$domain
  resolvectl query host.$domain     # answered by $node_ip via $iface
EOF
}

cmd_status() {
	local nwid
	if has_controller && nwid=$(find_network); then
		local reply
		if reply=$(api_call GET "/network/$nwid" 2>/dev/null); then
			jq '{id, name, status, portDeviceName, allowDNS, assignedAddresses, dns}' <<<"$reply"
			if command -v resolvectl >/dev/null; then
				resolvectl status "$(jq -r .portDeviceName <<<"$reply")" || true
			fi
		else
			echo "Network $nwid exists on the controller but is not joined."
		fi
	else
		echo "No $network_name network on this node's controller."
	fi
}

cmd_down() {
	has_controller || die "this node has no controller, so leave your my.zerotier.com network with: go run ./cmd/ztctl leave <id>"
	local nwid
	nwid=$(find_network) || {
		echo "No $network_name network found, so there is nothing to do."
		return 0
	}
	api_call DELETE "/network/$nwid" >/dev/null
	api_call DELETE "/controller/network/$nwid" >/dev/null
	echo "Left and deleted network $nwid."
}

case ${1:-up} in
up) cmd_up ;;
status) cmd_status ;;
down) cmd_down ;;
-h | --help) sed -n '2,27p' "$0" | sed 's/^# \{0,1\}//' ;;
*) die "unknown command '$1' (use up, status or down)" ;;
esac
