# nova-tools roadmap

Open work on the kept tools. Items about tools that left this repository went with them (docs/MOVED.md).

### The old-age remedy names the platform's own install path

nova-secrets tells Linux users to use Homebrew. It should name the install path for each platform, pinned by a test.

Target: v1.2.14

From: issue #5016

### nova-sandbox toolchain flag on darwin for cc, make and sqlite3

A flag adds the narrowest measured roots for each toolchain leg inside the wall, so the C compiler and make work on darwin. The native default uses the go leg with the same roots given to the harness fence.

Target: after v1.4

From: issue #1662

### Close the open nova-sandbox gaps on Linux before certifying walled legs

A private writable tmp under the Landlock policy and the related open defects must close first. Until then a Linux machine certifies only legs that need no wall.

Target: v1.5

From: issue #1664; issue #1495; issue #1737; issue #2162

### nova-sandbox reports the right cause and the right backend

An ssh host alias origin is reported as an unreachable forge, and a dot repo is refused. The policy verb prints the darwin profile under the landlock backend.

Target: v1.5

From: issue #1452; issue #1469
