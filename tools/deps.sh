#!/bin/sh

# Copyright 2022 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -o errexit
set -o nounset

#
# We will copy all dependencies for CSI Node driver to /dest directory
# all utils are using by csi-plugin
# to format/mount/unmount/resize the volumes.
#
# It is very important to have slim image,
# because it runs as root (privileged mode) on the nodes
#

DEST=/dest

copy_deps() {
    PROG="$1"

    mkdir -p "${DEST}$(dirname $PROG)"

    if [ -d "${PROG}" ]; then
        rsync -av "${PROG}/" "${DEST}${PROG}/"
    else
        cp -Lv "$PROG" "${DEST}${PROG}"
    fi

    if [ -x ${PROG} -o $(/usr/bin/ldd "$PROG" >/dev/null) ]; then
        DEPS="$(/usr/bin/ldd "$PROG" | /bin/grep '=>' | /usr/bin/awk '{ print $3 }')"

        for d in $DEPS; do
            mkdir -p "${DEST}$(dirname $d)"
            cp -Lv "$d" "${DEST}${d}"
        done
    fi
}

# This utils are using by
# go mod k8s.io/mount-utils
copy_deps /etc/mke2fs.conf
copy_deps /bin/mount
copy_deps /bin/umount
copy_deps /sbin/blkid
copy_deps /sbin/blockdev
copy_deps /sbin/dumpe2fs
copy_deps /sbin/fsck
copy_deps /sbin/fsck.xfs
cp /sbin/fsck* ${DEST}/sbin/
copy_deps /sbin/e2fsck
# from pkg e2fsprogs - e2image, e2label, e2scrub and etc.
cp /sbin/e* ${DEST}/sbin/
copy_deps /sbin/mke2fs
copy_deps /sbin/resize2fs
cp /sbin/mkfs* ${DEST}/sbin/
copy_deps /sbin/mkfs.xfs
copy_deps /sbin/xfs_repair
copy_deps /usr/sbin/xfs_growfs
copy_deps /usr/sbin/xfs_io
cp /usr/sbin/xfs* ${DEST}/usr/sbin/

# This utils are using by
# go mod pkg/csi/node.go
copy_deps /sbin/fstrim
copy_deps /sbin/cryptsetup
ARCH=$(uname -m)
mkdir -p ${DEST}/lib/${ARCH}-linux-gnu && cp /lib/${ARCH}-linux-gnu/libgcc_s.so.* ${DEST}/lib/${ARCH}-linux-gnu/

# hack for fsck https://github.com/sergelogvinov/proxmox-csi-plugin/issues/59
copy_deps /bin/true
rm -f ${DEST}/sbin/fsck.xfs
ln -s /bin/true ${DEST}/sbin/fsck.xfs

# This utils are using by
# go mod k8s.io/cloud-provider-openstack/pkg/util/mount
copy_deps /bin/udevadm
copy_deps /lib/udev/rules.d
copy_deps /bin/findmnt
