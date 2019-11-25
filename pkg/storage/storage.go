package storage

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"
	metainternalversion "k8s.io/apimachinery/pkg/apis/meta/internalversion"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/apiserver/pkg/endpoints/request"
	"k8s.io/apiserver/pkg/registry/rest"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog"
)

//func NewREST() rest.StandardStorage {
func NewREST(extR, intR GroupVersionKindResource, nsScoped bool, client dynamic.Interface, shortNames, categories []string) rest.Storage {
	return &restStorage{
		mapper: &mapper{
			External: extR,
			Internal: intR,
		},
		categories:      categories,
		shortNames:      shortNames,
		namespaceScoped: nsScoped,
		client: client.Resource(schema.GroupVersionResource{
			Group:    intR.GroupVersion.Group,
			Version:  intR.GroupVersion.Version,
			Resource: intR.Resource,
		}),
	}
}

func (r *restStorage) Categories() []string {
	return r.categories
}

func (r *restStorage) ShortNames() []string {
	return r.shortNames
}

type GroupVersionKindResource struct {
	schema.GroupVersion
	Kind     string
	Resource string
}

func (r *GroupVersionKindResource) AssignList(o runtime.Object) *unstructured.UnstructuredList {
	ul := o.(*unstructured.UnstructuredList)
	ul.SetAPIVersion(r.GroupVersion.String())
	ul.SetKind(r.Kind + "List")
	for _, item := range ul.Items {
		r.Assign(&item)
	}
	return ul
}

func (r *GroupVersionKindResource) Assign(o runtime.Object) *unstructured.Unstructured {
	u := o.(*unstructured.Unstructured)
	u.SetAPIVersion(r.GroupVersion.String())
	u.SetKind(r.Kind)
	return u
}

type mapper struct {
	External GroupVersionKindResource
	Internal GroupVersionKindResource
}

type restStorage struct {
	categories      []string
	shortNames      []string
	mapper          *mapper
	namespaceScoped bool
	client          dynamic.NamespaceableResourceInterface
}

type watcher struct {
	wi      watch.Interface
	mapper  func(runtime.Object) *unstructured.Unstructured
	wrapper *watch.RaceFreeFakeWatcher
}

func newWrappedWatcher(mapper func(o runtime.Object) *unstructured.Unstructured, wi watch.Interface) *watcher {
	w := &watcher{
		mapper:  mapper,
		wi:      wi,
		wrapper: watch.NewRaceFreeFake(),
	}
	go w.run()
	return w
}

func (w *watcher) run() {
	defer w.wrapper.Stop()
	for e := range w.wi.ResultChan() {
		if e.Type != watch.Error {
			w.wrapper.Action(e.Type, w.mapper(e.Object))
		} else {
			w.wrapper.Action(e.Type, e.Object)
		}
	}
}

func (w *watcher) ResultChan() <-chan watch.Event {
	return w.wrapper.ResultChan()
}

func (w *watcher) Stop() {
	w.wi.Stop()
	w.wrapper.Stop()
}

func (r *restStorage) Create(ctx context.Context, obj runtime.Object, createValidation rest.ValidateObjectFunc, options *metav1.CreateOptions) (runtime.Object, error) {
	if err := createValidation(obj); err != nil {
		return nil, err
	}
	orig := r.mapper.Internal.Assign(obj)
	if options != nil {
		options = &metav1.CreateOptions{}
	}
	created, err := r.getClient(ctx).Create(orig, *options)
	if err != nil {
		return nil, err
	}
	return r.mapper.External.Assign(created), nil
}

func (r *restStorage) Update(ctx context.Context, name string, objInfo rest.UpdatedObjectInfo, createValidation rest.ValidateObjectFunc, updateValidation rest.ValidateObjectUpdateFunc, forceAllowCreate bool, options *metav1.UpdateOptions) (runtime.Object, bool, error) {
	o, err := r.Get(ctx, name, nil)
	if err != nil {
		if errors.IsNotFound(err) && forceAllowCreate {
			// We have the external version which is what we want to run validations on.
			newObj, err := objInfo.UpdatedObject(ctx, r.New())
			if err != nil {
				return nil, false, err
			}
			c, err := r.Create(ctx, newObj, createValidation, nil)
			if err != nil {
				return nil, false, err
			}
			return c, true, nil
		}
		return nil, false, err
	}
	// We have the external version which is what we want to run validations on.
	updated, err := objInfo.UpdatedObject(ctx, o)
	if err != nil {
		return nil, false, err
	}

	orig := r.mapper.Internal.Assign(updated)

	// Run precondition checks.
	if objInfo.Preconditions != nil {
		if pc := objInfo.Preconditions(); pc != nil {
			if pc.UID != nil && *pc.UID != orig.GetUID() {
				return nil, false, fmt.Errorf("failed uid precondition")
			}
			if pc.ResourceVersion != nil && *pc.ResourceVersion != orig.GetResourceVersion() {
				return nil, false, fmt.Errorf("failed resourceVersion precondition")
			}
		}
	}

	if options != nil {
		options = &metav1.UpdateOptions{}
	}
	returned, err := r.getClient(ctx).Update(orig, *options)
	if err != nil {
		return nil, false, err
	}
	r.mapper.External.Assign(returned)
	return returned, false, nil
}

// NewList returns an empty object that can be used with the List call.
// This object must be a pointer type for use with Codec.DecodeInto([]byte, runtime.Object)
func (r *restStorage) NewList() runtime.Object {
	ul := &unstructured.UnstructuredList{}
	r.mapper.External.AssignList(ul)
	return ul
}

// New returns an empty object that can be used with Create after request data has been put into it.
// This object must be a pointer type for use with Codec.DecodeInto([]byte, runtime.Object)
func (r *restStorage) New() runtime.Object {
	u := &unstructured.Unstructured{}
	r.mapper.External.Assign(u)
	return u
}

func (r *restStorage) Watch(ctx context.Context, options *metainternalversion.ListOptions) (watch.Interface, error) {
	lo, err := toMetaListOptions(options)
	if err != nil {
		return nil, err
	}
	wi, err := r.getClient(ctx).Watch(lo)
	if err != nil {
		return nil, err
	}
	return newWrappedWatcher(r.mapper.External.Assign, wi), nil
}

func toMetaListOptions(options *metainternalversion.ListOptions) (metav1.ListOptions, error) {
	var lo metav1.ListOptions
	if options == nil {
		return lo, nil
	}
	if err := metainternalversion.Convert_internalversion_ListOptions_To_v1_ListOptions(options, &lo, nil); err != nil {
		return lo, err
	}
	return lo, nil
}

func (r *restStorage) getClient(ctx context.Context) dynamic.ResourceInterface {
	if ns, ok := request.NamespaceFrom(ctx); ok {
		return r.client.Namespace(ns)
	}
	return r.client
}

// List selects resources in the storage which match to the selector. 'options' can be nil.
func (r *restStorage) List(ctx context.Context, options *metainternalversion.ListOptions) (runtime.Object, error) {
	lo, err := toMetaListOptions(options)
	if err != nil {
		return nil, err
	}
	ul, err := r.getClient(ctx).List(lo)
	if err != nil {
		return nil, err
	}
	r.mapper.External.AssignList(ul)
	return ul, nil
}

func (r *restStorage) NamespaceScoped() bool {
	return r.namespaceScoped
}

// Get finds a resource in the storage by name and returns it.
// Although it can return an arbitrary error value, IsNotFound(err) is true for the
// returned error value err when the specified resource is not found.
func (r *restStorage) Get(ctx context.Context, name string, options *metav1.GetOptions) (runtime.Object, error) {
	if options == nil {
		options = &metav1.GetOptions{}
	}
	u, err := r.getClient(ctx).Get(name, *options)
	if err != nil {
		return nil, err
	}
	r.mapper.External.Assign(u)
	return u, nil
}

// Delete finds a resource in the storage and deletes it.
// The delete attempt is validated by the deleteValidation first.
// If options are provided, the resource will attempt to honor them or return an invalid
// request error.
// Although it can return an arbitrary error value, IsNotFound(err) is true for the
// returned error value err when the specified resource is not found.
// Delete *may* return the object that was deleted, or a status object indicating additional
// information about deletion.
// It also returns a boolean which is set to true if the resource was instantly
// deleted or false if it will be deleted asynchronously.
func (r *restStorage) Delete(ctx context.Context, name string, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions) (runtime.Object, bool, error) {
	obj, err := r.Get(ctx, name, &metav1.GetOptions{})
	if err != nil {
		return nil, false, err
	}
	if err := deleteValidation(obj); err != nil {
		return nil, false, err
	}
	orig := r.mapper.Internal.Assign(obj)
	if err := r.getClient(ctx).Delete(orig.GetName(), options); err != nil {
		return nil, false, err
	}
	return r.mapper.External.Assign(orig), false, nil
}

// DeleteCollection selects all resources in the storage matching given 'listOptions'
// and deletes them. The delete attempt is validated by the deleteValidation first.
// If 'options' are provided, the resource will attempt to honor them or return an
// invalid request error.
// DeleteCollection may not be atomic - i.e. it may delete some objects and still
// return an error after it. On success, returns a list of deleted objects.
func (r *restStorage) DeleteCollection(ctx context.Context, deleteValidation rest.ValidateObjectFunc, options *metav1.DeleteOptions, listOptions *metainternalversion.ListOptions) (runtime.Object, error) {
	l, err := r.List(ctx, listOptions)
	if err != nil {
		klog.Error(err)
		return nil, err
	}
	deleted := r.NewList().(*unstructured.UnstructuredList)
	ul := l.(*unstructured.UnstructuredList)
	for _, i := range ul.Items {
		r, _, err := r.Delete(ctx, i.GetName(), deleteValidation, options)
		if err != nil {
			klog.Error(err)
			return deleted, err
		}
		deleted.Items = append(deleted.Items, (*r.(*unstructured.Unstructured)))
	}
	klog.Info("done deleting")
	return deleted, nil
}
