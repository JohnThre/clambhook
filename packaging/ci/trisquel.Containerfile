FROM scratch

# Downloaded and SHA-256 verified by scripts/validate-linux-distros.sh from
# Trisquel's official Ecne build archive. OCI builders extract the bzip2-
# compressed root filesystem when ADD processes the local archive.
ADD trisquel-base_12.0_arm64.tar.bz2 /

CMD ["/bin/bash"]
