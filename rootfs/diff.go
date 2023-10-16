/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package rootfs

import (
	"context"
	"fmt"

	imagespec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd/diff"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/pkg/cleanup"
	"github.com/containerd/containerd/snapshots"
)

// CreateDiff creates a layer diff for the given snapshot identifier from the
// parent of the snapshot. A content ref is provided to track the progress of
// the content creation and the provided snapshotter and mount differ are used
// for calculating the diff. The descriptor for the layer diff is returned.
func CreateDiff(ctx context.Context, snapshotID string, sn snapshots.Snapshotter, d diff.Comparer, opts ...diff.Opt) (imagespec.Descriptor, error) {
	info, err := sn.Stat(ctx, snapshotID)
	if err != nil {
		return imagespec.Descriptor{}, err
	}

	lowerKey := fmt.Sprintf("%s-parent-view-%s", info.Parent, uniquePart())
	lower, err := sn.View(ctx, lowerKey, info.Parent)
	if err != nil {
		return imagespec.Descriptor{}, err
	}
	defer cleanup.Do(ctx, func(ctx context.Context) {
		sn.Remove(ctx, lowerKey)
	})

	var upper []mount.Mount
	if info.Kind == snapshots.KindActive {
		upper, err = sn.Mounts(ctx, snapshotID)
		if err != nil {
			return imagespec.Descriptor{}, err
		}
	} else {
		upperKey := fmt.Sprintf("%s-view-%s", snapshotID, uniquePart())
		upper, err = sn.View(ctx, upperKey, snapshotID)
		if err != nil {
			return imagespec.Descriptor{}, err
		}
		defer cleanup.Do(ctx, func(ctx context.Context) {
			sn.Remove(ctx, upperKey)
		})
	}

	return d.Compare(ctx, lower, upper, opts...)
}
