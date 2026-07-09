package filter

type FilterFunc func(raw, command string) string

var filters []struct {
	prefix string
	fn     FilterFunc
}

func Register(prefix string, fn FilterFunc) {
	filters = append(filters, struct {
		prefix string
		fn     FilterFunc
	}{prefix, fn})
}

func Run(raw, command string) string {
	for _, f := range filters {
		if hasPrefix(command, f.prefix) {
			return f.fn(raw, command)
		}
	}
	return noopFilter(raw, command)
}

func hasPrefix(cmd, prefix string) bool {
	cmd = trimExe(cmd)
	return len(cmd) >= len(prefix) && cmd[:len(prefix)] == prefix
}

func trimExe(cmd string) string {
	if len(cmd) > 4 && cmd[len(cmd)-4:] == ".exe" {
		return cmd[:len(cmd)-4]
	}
	return cmd
}
