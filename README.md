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
- configure timeout
- configure additional announce options? (downloaded, uploaded, left, event, ...)


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

This is what a typical call might look like:

    tbomb -c 200 -u localhost:6882 -t 30 -k -e

This is what a typical report might look like:

    Dispatching 200 clients
    Waiting for results...
    
    Requests:                           489671
    Successful requests:                487957
    failed requests:                      1714
    Connect attempts:                      215
    Failed connects:                        15
    Successful requests rate:            15259 hits/sec
    Test time:                           31.98 sec
    Encountere errors:
        announce: I/O timeout on receive        1714
        connect: I/O timeout on receive          15
    
Note that the test actually took about two seconds longer than specified - this is due to the receive timeout being two seconds.  
Also note this: If (1714 + 15) = 1729 timeouts occurred, each after two seconds, 3458 seconds in total were timed out. With 200
concurrent clients over 30 seconds (=6000 seconds of work), about half of the time was actually blocked due to timeouts.


## License
MIT