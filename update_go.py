import re
with open('internal/nsprint/task/take.go', 'r') as f:
    go_code = f.read()

new_take_avail = """func TakeAvailable(ctx context.Context, st *store.Store, as, sprint, id string, limit int, actor, idem string) ([]Claim, error) {
	if st == nil || as == "" || limit < 0 {
		return nil, fmt.Errorf("task take: store, as and nonnegative n are required")
	}
	client := st.Client()

	strict := "0"
	if id != "" {
		strict = "1"
	}
	
	maxClaims := limit
	if maxClaims == 0 {
	    maxClaims = 12
	}
	
	args := []any{as, strconv.Itoa(limit), strict, actor, idem, sprint, id}
	for i := 0; i < maxClaims; i++ {
		random, err := RandomToken()
		if err != nil {
			return nil, err
		}
		args = append(args, random)
	}

	reply, err := client.FCall(ctx, FunctionTakeN, nil, args...).Result()
	if err != nil {
		return nil, fmt.Errorf("task take: %w", err)
	}
	
	entries, ok := reply.([]any)
	if !ok {
		return nil, fmt.Errorf("task take: unexpected batch reply %T", reply)
	}
	claims := make([]Claim, 0, limit)
	var retry []TakeRequest
	for _, entry := range entries {
		values, ok := entry.([]any)
		if !ok || len(values) == 0 {
			return claims, fmt.Errorf("task take: unexpected batch entry %T", entry)
		}
		status := fmt.Sprint(values[0])
		if status == "NOTFRIEND" {
			return nil, ErrNotFriend
		}
		if status == TakeClaimed {
			claim, err := parseClaim(values)
			if err != nil {
				return claims, err
			}
			claims = append(claims, claim)
			continue
		}
		if len(values) < 4 {
			return claims, fmt.Errorf("task take: short batch entry %v", values)
		}
		name, taskID, detail := fmt.Sprint(values[1]), fmt.Sprint(values[2]), fmt.Sprint(values[3])
		switch status {
		case TakeBlocked:
			if id != "" {
				return claims, &BlockedError{Sprint: name, ID: taskID, Needs: strings.Fields(detail)}
			}
		case "RETRY":
			retry = append(retry, TakeRequest{Sprint: name, ID: taskID, As: as, Actor: actor, Idem: idem})
		case "DOWN", "FULL":
			return claims, fmt.Errorf("task take %s: friend %s is %s", taskID, as, status)
		default:
			return claims, fmt.Errorf("task take %s: unexpected status %q", taskID, status)
		}
	}
	for _, req := range retry {
		if limit != 0 && len(claims) == limit {
			break
		}
		claim, ok, err := Take(ctx, st, req)
		var blocked *BlockedError
		if id == "" && errors.As(err, &blocked) {
			continue
		}
		if err != nil {
			return claims, err
		}
		if ok {
			claims = append(claims, claim)
		}
	}
	return claims, nil
}"""

go_code = re.sub(r'func TakeAvailable\(ctx context\.Context,.*?return claims, nil\n\}', new_take_avail, go_code, flags=re.DOTALL)

with open('internal/nsprint/task/take.go', 'w') as f:
    f.write(go_code)
