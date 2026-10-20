// Package install carries the one-command install the CLI pipes to bash
// over ssh (docs/workstreams/SPINE.md Contract 5). The script lives
// beside this file because go:embed reads only the package's own
// directory, and the script is what `repose machine add` sends: nothing
// has to be on the box first.
package install

import _ "embed"

// Script is ops/install/machine-install.sh.
//
//go:embed machine-install.sh
var Script string
