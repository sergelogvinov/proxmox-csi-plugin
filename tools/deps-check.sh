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

# k8s.io/mount-utils
/bin/mount -V
/bin/umount -V
/sbin/blkid -V
/sbin/blockdev -V
/sbin/dumpe2fs -V
/sbin/fsck --version
/sbin/mke2fs -V
/sbin/mkfs.ext4 -V
/sbin/mkfs.xfs -V
/usr/sbin/xfs_io -V
# /sbin/resize2fs
/sbin/xfs_repair -V
/usr/sbin/xfs_growfs -V

# pkg/csi/node.go
/sbin/fstrim -V

# k8s.io/cloud-provider-openstack/pkg/util/mount
/bin/udevadm --version
/bin/findmnt -V
