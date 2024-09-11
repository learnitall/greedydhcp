# greedydhcp

Run a DHCP client that continually requests the same address(es).
Edit the `TARGET_ADDRS` environment variable to be a CSV of
the desired addressrs and run on the host.

When a lease for a target address is acquired, it will be held
by the binary but it will not be bound to an interface or used by
the host in any way.
