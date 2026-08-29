// SPDX-FileCopyrightText: 2026 Pengfan Chang <support@swiphtgroup.com>
// SPDX-License-Identifier: GPL-3.0-only

#ifndef CLAMBHOOK_GEO_H
#define CLAMBHOOK_GEO_H

#include "clambhook/config.h"
#include "clambhook/error.h"

typedef struct ch_geo_reader ch_geo_reader;

typedef struct ch_geo_location {
    char *country;
    char *country_code;
    char *city;
    double latitude;
    double longitude;
} ch_geo_location;

/* A missing/empty root [geo].database returns CH_OK with a NULL reader. */
ch_status ch_geo_reader_open_config(const ch_config *config,
                                    ch_geo_reader **out_reader,
                                    ch_error *error);
void ch_geo_reader_retain(ch_geo_reader *reader);
void ch_geo_reader_release(ch_geo_reader *reader);

/* A NULL reader is a successful disabled lookup with an empty location. */
ch_status ch_geo_lookup(ch_geo_reader *reader, const char *address,
                        ch_geo_location *out_location, ch_error *error);
void ch_geo_location_clear(ch_geo_location *location);

#endif
