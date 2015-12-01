# tbomb

`tbomb` is a utility to stress-test bittorrent trackers via UDP.

It (already!) has some cool features:

- Pre-allocated packets (performance)
- can reuse connection IDs (much like HTTP keepalive)
- reports what errors occurred, and how many of them
- random peer IDs
- random infohashes
- checks transaction IDs
- can finish after either a specified time or a specified number of requests

Planned features:

- Scrape mode (with multi-scrape)
- Some way to limit the amount of infohashes/peerIDs generated
- IPv6 mode? Maybe when IPv6 for UDP trackers is defined.


It was inspired by [gobench](https://github.com/cmpxchg16/gobench).

## How to get it
To get it, simply

    go get -u github.com/mrd0ll4r/tbomb

That should download and install it.

## Usage
The basic usage is

    tbomb [options] (-r <requests per client>|-t <timeout>) -u <target>

The options and arguments are:

    -u <target>: specifies the target to test, e.g. 127.0.0.1:12345 or tracker.example.org:1337
    -t <timeout>: specifies the period of testing in seconds
    -r <requests>: specifies the number of requests to send _per_client_
    -c <clients>: specifies the number of concurrent clients to use
    -k: enables keep-alive mode (reusing connectionIDs)
    -e: enables detailed error reports (has some impact on performance)
    (-s: enables scrape-mode (not yet implemented))


## Lincese
MIT