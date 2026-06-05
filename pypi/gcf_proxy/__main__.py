"""Entry point for `python -m gcf_proxy` and `gcf-proxy` CLI."""

import os
import sys
import subprocess


def _find_binary():
    """Locate the gcf-proxy binary bundled in this package."""
    pkg_dir = os.path.dirname(os.path.abspath(__file__))
    names = ["gcf-proxy.exe", "gcf-proxy"] if sys.platform == "win32" else ["gcf-proxy"]
    for name in names:
        path = os.path.join(pkg_dir, "bin", name)
        if os.path.isfile(path):
            return path
    return None


def main():
    binary = _find_binary()
    if binary is None:
        print(
            "gcf-proxy: binary not found. This platform may not be supported.\n"
            "Install from https://github.com/blackwell-systems/gcf-proxy/releases",
            file=sys.stderr,
        )
        sys.exit(1)

    result = subprocess.run([binary] + sys.argv[1:])
    sys.exit(result.returncode)


if __name__ == "__main__":
    main()
