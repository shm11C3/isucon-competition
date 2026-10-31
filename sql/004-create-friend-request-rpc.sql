-- Install once before deploying the caller; deliberately fail if already installed.
-- VOLATILE gives each internal statement a fresh READ COMMITTED snapshot.
CREATE FUNCTION create_friend_request_v1(p_actor text, p_target text, p_request_id text, p_friendship_id text)
RETURNS TABLE(id varchar(36), status text, updated_at timestamptz)
LANGUAGE plpgsql VOLATILE SECURITY INVOKER
AS $$
DECLARE
  locked_count integer;
  existing friend_requests%ROWTYPE;
  result_id text;
BEGIN
  IF p_actor = p_target OR p_target = '' THEN
    RAISE EXCEPTION 'invalid relationship' USING ERRCODE = 'PR400';
  END IF;
  PERFORM u.id FROM users u
    WHERE u.id IN (p_actor, p_target) AND u.status = 'active'
    ORDER BY u.id COLLATE "C" FOR NO KEY UPDATE;
  GET DIAGNOSTICS locked_count = ROW_COUNT;
  IF locked_count <> 2 THEN
    RAISE EXCEPTION 'relationship not found' USING ERRCODE = 'PR404';
  END IF;
  -- These must stay separate from the lock query: a lock wait must be
  -- followed by a new snapshot to observe the preceding owner's commit.
  IF EXISTS (SELECT 1 FROM block_relations b
    WHERE (b.blocker_id = p_actor AND b.blocked_id = p_target)
       OR (b.blocker_id = p_target AND b.blocked_id = p_actor)) THEN
    RAISE EXCEPTION 'relationship conflict' USING ERRCODE = 'PR409';
  END IF;
  IF EXISTS (SELECT 1 FROM friendships f
    WHERE (f.user_low_id = p_actor AND f.user_high_id = p_target)
       OR (f.user_low_id = p_target AND f.user_high_id = p_actor)) THEN
    RAISE EXCEPTION 'relationship conflict' USING ERRCODE = 'PR409';
  END IF;
  SELECT r.* INTO existing FROM friend_requests r WHERE r.status = 'pending'
    AND ((r.requester_id = p_actor AND r.recipient_id = p_target)
      OR (r.requester_id = p_target AND r.recipient_id = p_actor));
  IF FOUND THEN
    IF existing.requester_id = p_actor THEN
      RAISE EXCEPTION 'relationship conflict' USING ERRCODE = 'PR409';
    END IF;
    UPDATE friend_requests r SET status = 'accepted', updated_at = clock_timestamp() WHERE r.id = existing.id;
    INSERT INTO friendships (id, user_low_id, user_high_id, source_request_id)
      VALUES (p_friendship_id,
        LEAST(existing.requester_id COLLATE "C", existing.recipient_id COLLATE "C"),
        GREATEST(existing.requester_id COLLATE "C", existing.recipient_id COLLATE "C"), existing.id);
    result_id := existing.id;
  ELSE
    INSERT INTO friend_requests (id, requester_id, recipient_id, status, created_at, updated_at)
      VALUES (p_request_id, p_actor, p_target, 'pending', clock_timestamp(), clock_timestamp());
    result_id := p_request_id;
  END IF;
  RETURN QUERY SELECT r.id, r.status, r.updated_at FROM friend_requests r WHERE r.id = result_id;
END;
$$;
