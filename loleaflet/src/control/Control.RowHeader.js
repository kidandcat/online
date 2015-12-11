/*
 * L.Control.RowHeader
*/

L.Control.RowHeader = L.Control.extend({
	onAdd: function (map) {
		map.on('updatepermission', this._onUpdatePermission, this);
		this._initialized = false;
		return document.createElement('div');
	},

	_initialize: function () {
		this._initialized = true;
		this._map.on('scrolloffset', this.offsetScrollPosition, this);
		this._map.on('updatescrolloffset', this.setScrollPosition, this);
		this._map.on('updateviewport', this.setViewPort, this);
		this._map.on('viewrowcolumnheaders', this.viewRowColumnHeaders, this);
		var docContainer = this._map.options.documentContainer;
		var divRowHeader = L.DomUtil.create('div', 'spreadsheet-container-row', docContainer.parentElement);
		this._table = L.DomUtil.create('table', 'spreadsheet-table-row', divRowHeader);
		this._rows = L.DomUtil.create('tbody', '', this._table);

		this._position = 0;
		this._totalHeight = 0;
		this._viewPort = 0;

		// dummy initial row header
		var trRow = L.DomUtil.create('tr', '', this._rows);
		var thRow = L.DomUtil.create('th', 'spreadsheet-table-row-cell', trRow);
		L.DomUtil.create('div', 'spreadsheet-table-row-cell-text', thRow);
	},

	clearRows: function () {
		L.DomUtil.remove(this._rows);
		this._rows = L.DomUtil.create('tbody', '', this._table);
	},

	setViewPort: function(e) {
		this._viewPort = e.rows.viewPort;
		this._totalHeight = e.rows.totalHeight;
	},

	setScrollPosition: function (e) {
		var position = -e.y;
		this._position = Math.min(0, position);
		L.DomUtil.setStyle(this._table, 'top', this._position + 'px');
	},

	offsetScrollPosition: function (e) {
		var offset = e.y;
		this._position = Math.min(0,
		Math.max(this._position - offset,
			-(this._totalHeight - this._viewPort)));
		L.DomUtil.setStyle(this._table, 'top', this._position + 'px');
	},

	viewRowColumnHeaders: function (e) {
		if (e.isZoomed) {
			this.updateRows(e.data.rows, e.converter, e.context);
		}
		else {
			this.fillRows(e.data.rows, e.converter, e.context);
		}
		this._map._docLayer._isZoomed = false;
	},

	fillRows: function (rows, converter, context) {
		var iterator, twip, height, row, cell, text;

		this.clearRows();
		for (iterator = 0; iterator < rows.length; iterator++) {
			height = rows[iterator].size - (iterator > 0 ? rows[iterator - 1].size : 0);
			twip = new L.Point(height, height);
			row  = L.DomUtil.create('tr', '', this._rows);
			cell = L.DomUtil.create('th', 'spreadsheet-table-row-cell', row);
			text = L.DomUtil.create('div', 'spreadsheet-table-row-cell-text', cell);
			text.innerHTML  = rows[iterator].text;
			height = Math.round(converter.call(context, twip).y) - (iterator > 0 ? 1 : 0) + 'px';
			L.DomUtil.setStyle(text, 'line-height', height);
			L.DomUtil.setStyle(text, 'height', height);
		}
	},

	updateRows: function (rows, converter, context) {
		var iterator, twip, height, text;

		for (iterator = 0; iterator < this._rows.childNodes.length; iterator++) {
			text  = this._rows.childNodes[iterator].firstChild.firstChild;
			height = rows[iterator].size - (iterator > 0 ? rows[iterator - 1].size : 0);
			twip = new L.Point(height, height);
			height = Math.round(converter.call(context, twip).y) - (iterator > 0 ? 1 : 0) + 'px';
			L.DomUtil.setStyle(text, 'line-height', height);
			L.DomUtil.setStyle(text, 'height', height);
		}
	},

	_onUpdatePermission: function () {
		if (this._map.getDocType() === 'spreadsheet' && !this._initialized) {
			this._initialize();
		}
	}
});

L.control.rowHeader = function (options) {
	return new L.Control.RowHeader(options);
};