#!/usr/bin/env python3
"""Prepare immutable assets and validated candidates; does not reload production."""
from frontend_release import prepare

if __name__ == '__main__':
    prepare()
