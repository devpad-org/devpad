// Package loginpath keeps a container's PATH in its login shells. A login
// shell's /etc/profile replaces PATH with Debian's default, so a project
// environment's ENV PATH would reach a plain shell but not bash -l, which
// commands, setup scripts and pipeline jobs run in. /etc/profile runs the
// scripts in /etc/profile.d after that and before ~/.profile, so one there
// puts the image's PATH back and whatever ~/.profile adds still goes in
// front of it. The workspace agent writes it when it starts; devpad writes
// it into a pipeline job's container, which has no agent.
package loginpath

import "strings"

// File is the script that restores the image's PATH.
const File = "/etc/profile.d/00-devpad-path.sh"

// Script returns a script setting PATH to path.
func Script(path string) []byte {
	quoted := "'" + strings.ReplaceAll(path, "'", `'\''`) + "'"
	return []byte("# Written by devpad: the PATH of this container's image.\nPATH=" + quoted + "\nexport PATH\n")
}
