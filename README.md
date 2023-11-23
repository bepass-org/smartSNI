# Smart SNI and DNS Proxy Server

This DNS Proxy Server is a Go-based server capable of handling both DNS-over-HTTPS (DoH) and DNS-over-TLS (DoT) requests. It features rate limiting and can process DNS queries based on a custom JSON configuration file.

## Features

- **DNS-over-HTTPS (DoH):** Accepts and processes DNS queries over HTTPS.
- **DNS-over-TLS (DoT):** Accepts and processes DNS queries over TLS.
- **Rate Limiting:** Throttles the number of requests using a limiter.
- **Custom Domain Handling:** Matches DNS queries to a list of specified domains and returns corresponding IP addresses.
- **SNI Proxy:** Proxies non-matching domains to their respective addresses.
- **Configurable:** Uses a `config.json` file to define behavior for specified domains.

## Configuration

The server uses a `config.json` file which should be structured as follows:

```json
{
  "host": "your.host.com",
  "domains": {
    "example.com": "1.2.3.4",
    "anotherdomain.com": "1.2.3.4"
  }
}
```

Replace the IP addresses with your server's public IP to ensure transparent proxying(Here it's 1.2.3.4).\
\
You can use this code to proxy all domains(its not recommended)

```json
{
  "host": "your.host.com",
  "domains": {
    ".": "1.2.3.4"
  }
}
```

## TLS Certificates

The DoT and DOH servers expect TLS certificates to be located at `/etc/letsencrypt/live/your.host.com/`. Make sure you have valid certificates named `fullchain.pem` and `privkey.pem`.\
\
You can obtain a valid certificate for your domain with lets encrypt


## Auto Install

```
bash <(curl -fsSL https://raw.githubusercontent.com/bepass-org/smartSNI/main/install.sh)
```

## Manual Setup

1. Install Requirements
```bash
apt update
apt install nginx certbot python3-certbot-nginx
snap install go --classic
```
2. Change server_name in /etc/nginx/sites-enabled/default to your `domain`
3. Obtain a valid certificate for nginx
```bash
certbot --nginx -d <YOUR_DOMAIN>
```
4. Clone the repository to your local machine.
5. Create and configure your `config.json` file.
6. Run `go build` to compile the server.
7. Run the compiled binary to start the server in tmux or in background with nohup.

```bash
./name-of-compiled-binary
```

## Rate Limiting

The server uses the `golang.org/x/time/rate` package to implement rate limiting. You can adjust the rate limiter in the `main` function to suit your needs.

## Contributions

Contributions to this project are welcome. Please fork the repository, make your changes, and submit a pull request.

## Credits

Special thanks to [Peyman](https://github.com/Ptechgithub) for auto install script

## License

This project is open-source and available under the [MIT License](LICENSE).
