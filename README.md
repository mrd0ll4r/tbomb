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

    Dispatching 400 clients
    Waiting for results...
    
    Requests:                           504547
    Successful requests:                499834
    failed requests:                      4713
    Connect attempts:                      460
    Failed connects:                        60
    Successful requests rate:            16658 hits/sec
    Approximate concurrency:            105.99 clients running
    Test time:                           30.01 sec
    Errors encountered:
        announce: I/O timeout on receive        4351
        connect: I/O timeout on receive          60

Note: To have the test results as accurate as possible, we set the read timeout to zero as soon as the test time is over.
This causes all blocking read operations to return immediately, so we don't get the two second delay (because we had
two seconds of timeout) any more. But it also causes all blocking read operations to return a timeout. We don't count them
in the errors encountered and we don't use them to calculate the approximate concurrency. But you will see more failed
requests/connects than errors encountered. Now you know why. The successful requests rate and test time are more accurate,
the approximate concurrency is less accurate (because we cancel *real* timeouts as well). This only applies if you 
specify a timeout for the test.

This is another typical report, it's from an older version. I keep it here to show how the approximate concurrency is
computed:

    Dispatching 200 clients
    Waiting for results...
    
    Requests:                           571644
    Successful requests:                569970
    failed requests:                      1674
    Connect attempts:                      254
    Failed connects:                        54
    Successful requests rate:            18054 hits/sec
    Approximate concurrency:             90.53 clients running
    Test time:                           31.57 sec
    Errors encountered:
        announce: I/O timeout on receive        1674
        connect: I/O timeout on receive          54

    
Note that the test actually took about two seconds longer than specified - this is due to the receive timeout being two 
seconds. (no longer applies)  
Note about the approximate concurrency: If 1674 + 54 = 1728 timeouts occurred, each after two seconds, 3456 seconds in 
total were timed out. With 200 concurrent clients over 31.57 seconds (=6914 seconds of work), about half of the time 
was actually blocked due to timeouts. The `approximate concurrency` value therefore indicates how many clients were 
working full-time, (without waiting for I/O), on average. The value is experimental. The value will only be shown if you
use a timeout for the test.


## License
MIT