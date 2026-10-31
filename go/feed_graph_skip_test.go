package isureal

import (
	"reflect"
	"testing"
	"time"
)

func TestDirectFeedsIndependentOfSecondDegreeGraph(t *testing.T) {
	for _, hasPosted := range []bool{false, true} {
		full := viewerContext{User: User{ID: "viewer"}, HasPostedToday: hasPosted,
			Friends:          map[string]bool{"friend": true, "blocked-friend": true},
			Blocked:          map[string]bool{"blocked-friend": true, "blocked-fof": true},
			FriendsOfFriends: map[string]bool{"friend": true, "fof": true, "blocked-fof": true},
			MutualFriends:    map[string]int{"friend": 3, "fof": 2, "blocked-fof": 1}}
		reduced := full
		reduced.FriendsOfFriends = map[string]bool{}
		reduced.MutualFriends = map[string]int{}
		for _, feed := range []string{"friends", "public"} {
			input := getFeedRequest{Feed: feed}
			q1, a1, e1 := nonRankedFeedQuery(full, nil, input, nil, "")
			q2, a2, e2 := nonRankedFeedQuery(reduced, nil, input, nil, "")
			if e1 != nil || e2 != nil || q1 != q2 || !reflect.DeepEqual(a1, a2) {
				t.Fatalf("%s SQL changed", feed)
			}
			for _, author := range []string{"viewer", "friend", "blocked-friend", "fof", "blocked-fof", "stranger"} {
				for _, visibility := range []string{"public", "friends", "friends_of_friends", "unknown"} {
					ended := time.Now()
					for _, windowEnd := range []*time.Time{nil, &ended} {
						post := Post{AuthorID: author, Visibility: visibility, WindowEndedAt: windowEnd}
						want := full.canView(post) && matchesFeed(full, nil, post, feed)
						got := reduced.canView(post) && matchesFeed(reduced, nil, post, feed)
						if got != want {
							t.Fatalf("%s/%s/%s changed visibility", feed, author, visibility)
						}
					}
				}
			}
		}
	}
}
