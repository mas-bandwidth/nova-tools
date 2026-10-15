package swarm

// pushedHeader is the header every card `nova-sprint card push` pushes today, verbatim from
// the quack-0925b run (nova-tools#3711): REPO: owner/name, BASE: <ref>, base-sha: <sha40>.
// Before #3711 the card-base reader (since removed as dead code) read none of it and the model was launched with no repo.
func pushedHeader(sha string) []byte {
	return []byte("RESULT: s00-0302-quack-hulk-flash sha=" + sha[:12] + "\n" +
		"KIND: fix\n" +
		"TYPE: code\n" +
		"REPO: mas-bandwidth/nova-tools\n" +
		"BASE: dev\n" +
		"base-sha: " + sha + "\n" +
		"PATHS: docs/quack/s00-0302-quack-hulk-flash.txt\n")
}
