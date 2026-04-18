CC ?= cc
AR ?= ar

SODIUM_OK := $(shell pkg-config --exists libsodium && echo ok)
ifneq ($(SODIUM_OK),ok)
$(error libsodium not found via pkg-config. Install it: `brew install libsodium` on macOS, `apt install libsodium-dev` on Debian/Ubuntu, `dnf install libsodium-devel` on Fedora. Ensure pkg-config can find libsodium.pc (e.g. PKG_CONFIG_PATH=/opt/homebrew/lib/pkgconfig on Apple Silicon))
endif

SODIUM_CFLAGS := $(shell pkg-config --cflags libsodium)
CFLAGS = -Wall -Wextra -O2 -fPIC $(SODIUM_CFLAGS)

SRCS = src/packet.c src/crypto.c src/netio.c
OBJS = $(SRCS:.c=.o)
LIB = libcnet.a

all: $(LIB)

$(LIB): $(OBJS)
	$(AR) rcs $@ $^

src/%.o: src/%.c include/cnet.h
	$(CC) $(CFLAGS) -Iinclude -c $< -o $@

clean:
	rm -f $(OBJS) $(LIB)

.PHONY: all clean
