CC ?= cc
AR ?= ar

SODIUM_OK := $(shell pkg-config --exists libsodium && echo ok)
ifneq ($(SODIUM_OK),ok)
$(error libsodium not found via pkg-config. Install it with `apt install libsodium-dev` on Debian/Ubuntu or `brew install libsodium` on macOS. Ensure pkg-config can find libsodium.pc.)
endif

SODIUM_CFLAGS := $(shell pkg-config --cflags libsodium)
OPENSSL_OK := $(shell pkg-config --exists openssl && echo ok)
ifneq ($(OPENSSL_OK),ok)
$(error OpenSSL not found via pkg-config. Install libssl-dev on Debian/Ubuntu, openssl-devel on Fedora, or openssl@3 with Homebrew.)
endif

OPENSSL_CFLAGS := $(shell pkg-config --cflags openssl)
CFLAGS = -Wall -Wextra -O2 -fPIC $(SODIUM_CFLAGS) $(OPENSSL_CFLAGS)

SRCS = src/crypto.c
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
