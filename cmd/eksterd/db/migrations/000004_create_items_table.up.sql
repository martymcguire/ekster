/*
 *  Ekster is a microsub server
 *  Copyright (c) 2021 The Ekster authors
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>.
 */

CREATE TABLE IF NOT EXISTS "items" (
    "id" int primary key generated always as identity,
    "channel_id" int references "channels" on delete cascade,
    "uid" varchar(512) not null unique,
    "is_read" int default 0,
    "data" jsonb,
    "created_at" timestamptz DEFAULT current_timestamp,
    "updated_at" timestamptz,
    "published_at" timestamptz
);
