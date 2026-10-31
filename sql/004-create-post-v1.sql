CREATE OR REPLACE FUNCTION public.create_post_v1(
  p_user_id text,
  p_post_id text,
  p_image_id text,
  p_image_data bytea,
  p_caption text,
  p_visibility text,
  p_location_kind text,
  p_latitude numeric,
  p_longitude numeric,
  p_locality text
)
RETURNS TABLE (
  post_id text,
  post_author_id text,
  posting_window_id text,
  image_id text,
  caption text,
  visibility text,
  location_kind text,
  latitude numeric,
  longitude numeric,
  locality text,
  posted_at timestamptz,
  on_time_deadline timestamptz,
  window_ended_at timestamptz,
  author_user_id text,
  author_username text,
  author_secret_key_hash text,
  author_display_name text,
  author_bio text,
  author_profile_image_id text,
  author_region text,
  author_account_type text,
  author_status text
)
LANGUAGE plpgsql
VOLATILE
PARALLEL UNSAFE
SECURITY INVOKER
CALLED ON NULL INPUT
AS $function$
DECLARE
  v_user public.users%ROWTYPE;
  v_window public.posting_windows%ROWTYPE;
  v_post public.posts%ROWTYPE;
  v_active bigint;
  v_deleted bigint;
  v_posted_at timestamptz;
BEGIN
  SELECT u.* INTO v_user
  FROM public.users AS u
  WHERE u.id = p_user_id AND u.status = 'active'
  FOR NO KEY UPDATE;
  IF NOT FOUND THEN
    RAISE EXCEPTION USING ERRCODE = 'PZ001', MESSAGE = 'active user not found';
  END IF;

  IF char_length(v_user.secret_key_hash) <> 64
     OR v_user.secret_key_hash !~ '^[0-9A-Fa-f]{64}$' THEN
    RAISE EXCEPTION USING ERRCODE = 'PZ002', MESSAGE = 'invalid image signing key';
  END IF;

  SELECT w.* INTO v_window
  FROM public.posting_windows AS w
  WHERE w.region = v_user.region AND w.ended_at IS NULL
  FOR SHARE;
  IF NOT FOUND THEN
    SELECT w.* INTO v_window
    FROM public.posting_windows AS w
    WHERE w.region = v_user.region AND w.ended_at IS NULL
    FOR SHARE;
    IF NOT FOUND THEN
      RAISE EXCEPTION USING ERRCODE = 'PZ409', MESSAGE = 'post slot unavailable';
    END IF;
  END IF;

  SELECT count(*) FILTER (WHERE p.deleted_at IS NULL),
         count(*) FILTER (WHERE p.deleted_at IS NOT NULL)
  INTO v_active, v_deleted
  FROM public.posts AS p
  JOIN public.posting_windows AS w ON w.id = p.posting_window_id
  WHERE p.author_id = p_user_id AND w.local_date = v_window.local_date;

  v_posted_at := pg_catalog.clock_timestamp();
  IF v_active <> 0 OR v_deleted >= 2 OR v_posted_at < v_window.starts_at THEN
    RAISE EXCEPTION USING ERRCODE = 'PZ409', MESSAGE = 'post slot unavailable';
  END IF;

  INSERT INTO public.image_objects (id, owner_user_id, data, media_type, created_at)
  VALUES (p_image_id, p_user_id, p_image_data, 'image/jpeg', v_posted_at);

  INSERT INTO public.posts (
    id, author_id, posting_window_id, image_id, caption, visibility,
    location_kind, latitude, longitude, locality, posted_at
  ) VALUES (
    p_post_id, p_user_id, v_window.id, p_image_id, p_caption, p_visibility,
    p_location_kind, p_latitude, p_longitude, p_locality, v_posted_at
  ) RETURNING * INTO v_post;

  RETURN QUERY SELECT
    v_post.id::text, v_post.author_id::text, v_post.posting_window_id::text,
    v_post.image_id::text, v_post.caption, v_post.visibility, v_post.location_kind,
    v_post.latitude, v_post.longitude, v_post.locality, v_post.posted_at,
    v_window.on_time_deadline, v_window.ended_at,
    v_user.id::text, v_user.username::text, v_user.secret_key_hash,
    v_user.display_name, v_user.bio, v_user.profile_image_id::text,
    v_user.region, v_user.account_type, v_user.status;
END;
$function$;
