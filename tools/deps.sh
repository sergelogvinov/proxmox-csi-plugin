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

# k8s.io/mount-utils
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
cp /sbin/e* ${DEST}/sbin/
copy_deps /sbin/mke2fs
copy_deps /sbin/resize2fs
cp /sbin/mkfs* ${DEST}/sbin/
copy_deps /sbin/mkfs.xfs
copy_deps /sbin/xfs_repair
copy_deps /usr/sbin/xfs_growfs
cp /usr/sbin/xfs* ${DEST}/usr/sbin/

# k8s.io/cloud-provider-openstack/pkg/util/mount
copy_deps /bin/udevadm
copy_deps /lib/udev/rules.d
copy_deps /bin/findmnt
