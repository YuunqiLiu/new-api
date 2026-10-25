#!/usr/bin/env python3
"""Apply one prepared frontend state, with automatic staged rollback on failure."""
import argparse
from frontend_release import apply

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--state', required=True)
    args = parser.parse_args()
    apply(args.state)
