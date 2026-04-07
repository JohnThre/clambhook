CC ?= cc
CFLAGS = -Wall -Wextra -O2 -fPIC
AR ?= ar

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
