# Pi via proxy-me

This directory is a portable Pi configuration. It keeps the client on the
OpenAI Responses API and sends requests to proxy-me instead of ChatGPT.

```sh
export PI_CODING_AGENT_DIR="$PWD/pi-config"
export PROXY_ME_API_KEY="<the API key accepted by proxy-me>"
pi
```

The API key is intentionally supplied through the environment. Do not copy a
ChatGPT OAuth token into this configuration: proxy-me authenticates clients
with its configured gateway key and selects the upstream Codex account itself.
