# greedydhcp

Run a DHCP client that continually requests the same address.
Edit the `TargetAddr` variable in `main.go` to the desired request
and run on the host.

When a lease for the target address is acquired, it will be held
by the binary but it will not be bound to an interface or used by
the host in any way.
