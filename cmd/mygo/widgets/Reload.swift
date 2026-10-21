// The library that mygo build puts among the frameworks of apps with
// widgets: package widget calls it through purego to reload a kind's
// widgets, as WidgetKit's WidgetCenter has no Objective-C interface.

import WidgetKit

@_cdecl("mygo_widgets_reload")
public func mygoWidgetsReload(_ kind: UnsafePointer<CChar>) {
    WidgetCenter.shared.reloadTimelines(ofKind: String(cString: kind))
}
