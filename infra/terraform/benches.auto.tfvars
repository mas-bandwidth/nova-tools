# The fleet on 2026-09-17. This card instantiates the bench module for `space`
# (the medium role: 16 cores, 440 GB) and imports nothing else. A new bench is
# one entry here and one apply; the module is additive, so `terraform state rm`
# or `destroy` on it leaves the hand-built fleet as it was.
benches = {
  space = {
    role    = "medium"
    address = "space"
    mac     = "-"
  }
}

seat_public_keys = {
  space = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINovaFleetSeatSpacePublicKeyPlaceholder space@seat"
}
