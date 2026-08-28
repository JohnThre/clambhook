# SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
# SPDX-License-Identifier: GPL-3.0-only

FROM scratch

# Downloaded and SHA-256 verified by scripts/validate-linux-distros.sh from
# Trisquel's official Ecne build archive. OCI builders extract the bzip2-
# compressed root filesystem when ADD processes the local archive.
ADD trisquel-rootfs.tar.bz2 /

CMD ["/bin/bash"]
