/*!-----------------------------------------------------------
 * Copyright (c) Microsoft Corporation. All rights reserved.
 * Version: 0.52.2(404545bded1df6ffa41ea0af4e8ddb219018c6c1)
 * Released under the MIT license
 * https://github.com/microsoft/vscode/blob/main/LICENSE.txt
 *-----------------------------------------------------------*/
// Compatibility stub for AMD loaders that request editor.main.nls.js.
// Monaco 0.52.2 moved NLS data to the _VSCODE_NLS_MESSAGES global;
// this file is no longer part of the upstream package but some AMD
// environments still request it as a companion module to editor.main.js.
// An empty define satisfies the require() without impacting the editor.
define("vs/editor/editor.main.nls", {});
