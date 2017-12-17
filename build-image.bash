#!/bin/bash

set -euo pipefail
cd $(dirname "$0")

if [ "$(id -u)" -ne 0 ]; then
  echo "must be root" >&1
  exit 1
fi

dd if=/dev/zero of=mini.ext2 bs=1M count=32
mkfs -t ext2 mini.ext2 <<<'y'
mount mini.ext2 /mnt
go build -o /mnt/list-files list-files.go
touch /mnt/{foo,bar,baz}
umount /mnt
