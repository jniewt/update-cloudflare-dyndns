# Cloudflare DNS Record Updater
This is a simple http server with a single endpoint that on access updates a DNS record in Cloudflare. It is intended 
to be used as a webhook for a dynamic DNS client, such as the one in FritzBox.

## Requirements
- Cloudflare API Token with permissions to read and edit Zone.DNS
- Reverse proxy for HTTPS (FritzBox seems to only support https for DynDNS updates)

## Usage
1. Run on your server: `update-cloudflare-dns`, optionally supply an address to bind on: `-addr :8081`. The server needs
    the `CLOUDFLARE_API_TOKEN` environment variable set.
2. Configure DynDNS update URL on the FritzBox to: `http://your-server:8081?zone=<domain>&ip=<ipaddr>` 
   and enter your zone in the domain name field.
3. Optionally, use the supplied systemd service file to run the updater as a service. Don't forget to adjust the 
   file to match your environment.

## Limitations
- Only supports IPv4 A records for now