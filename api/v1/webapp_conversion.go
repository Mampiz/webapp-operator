/*
Copyright 2026.

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

package v1

// Hub marks v1 as the conversion hub: every other version converts to and from
// it, and it is what gets stored in etcd.
//
// With N served versions, a hub means N-1 conversion implementations instead of
// N×(N-1) pairwise ones. The hub should be the newest stable version, so old
// versions carry the compatibility code and the current one stays clean.
func (*WebApp) Hub() {}
