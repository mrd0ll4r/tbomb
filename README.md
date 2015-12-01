# tbomb

`tbomb` is a utility to stress-test bittorrent trackers via UDP.

It (already!) has some cool features:

- Pre-allocated packets (performance)
- can reuse connection IDs (much like HTTP keepalive)
- random peer IDs
- random infohashes
- checks transaction IDs
- can finish after either a specified time or a specified number of requests

Planned features:

- Scrape mode (with multi-scrape)
- Some way to limit the amount of infohashes/peerIDs generated


It was inspired by [gobench](https://github.com/cmpxchg16/gobench).



## Lincese
MIT