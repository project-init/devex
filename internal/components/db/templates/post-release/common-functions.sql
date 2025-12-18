-- Run this as the admin user

CREATE OR REPLACE FUNCTION public.gen_random_uuid_v7() RETURNS uuid AS $$
DECLARE
-- 48 bits for timestamp in milliseconds since Unix epoch
ts_millis BIGINT := (extract(epoch FROM clock_timestamp()) * 1000)::BIGINT;
    -- 74 bits for random data
    rand_b1 BIGINT := trunc(random() * 2^12)::BIGINT; -- 12 bits
    rand_b2 BIGINT := trunc(random() * 2^62)::BIGINT; -- 62 bits
    -- Combine to form a 128-bit integer
    ts_hex TEXT;
    rand1_hex TEXT;
    rand2_hex TEXT;
    uuid TEXT;
BEGIN
    -- Convert timestamp to hex (12 hex digits = 48 bits)
    ts_hex := lpad(to_hex(ts_millis), 12, '0');
    -- Convert random parts to hex
    rand1_hex := lpad(to_hex(rand_b1), 4, '0');
    rand2_hex := lpad(to_hex(rand_b2), 16, '0');

    -- Construct UUID v7 string
RETURN concat(
        substr(ts_hex, 1, 8), '-',
        substr(ts_hex, 9, 4), '-',
        '7', substr(rand1_hex, 1, 3), '-', -- version 7
        substr(rand12_hex, 1, 4), '-',
        substr(rand2_hex, 5, 12)
       )::uuid;
END;
$$ LANGUAGE plpgsql;

-- Add comment on the function
COMMENT ON FUNCTION public.gen_random_uuid_v7() IS 'Generates a random UUID version 7';

-- Time Management
CREATE OR REPLACE FUNCTION public.update_updated_at()
RETURNS TRIGGER AS $$
BEGIN
NEW.updated_at = clock_timestamp();
RETURN NEW;
END;
$$ language 'plpgsql';

-- Add comment on the function
COMMENT ON FUNCTION public.update_updated_at() IS 'Updates the updated_at timestamp on a table.';