#!/bin/bash
# autoland reads lines from redis using nova-sprint line list
nova-sprint line list --repo "$REPO" --n "$PR" --head "$HEAD"
