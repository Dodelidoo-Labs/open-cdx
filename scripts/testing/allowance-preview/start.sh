#!/bin/sh
set -eu
if [ ! -d /preview-data ]; then seed-preview --dir /preview-data; fi
exec routerd
