-- Roll back the Go caller first, then remove the now-unused function.
DROP FUNCTION create_friend_request_v1(text, text, text, text);
