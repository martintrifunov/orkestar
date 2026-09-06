package tui

// resolveNotifier returns nothing on Windows.
//
// A toast needs a registered AppUserModelID or a PowerShell module that is not
// installed by default, and shipping something that silently fails on most
// machines is worse than not claiming the feature. The bell still rings.
func resolveNotifier() notifier { return notifier{} }
