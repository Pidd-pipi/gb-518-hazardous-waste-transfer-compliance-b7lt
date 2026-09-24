import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, OnChanges, Output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import type { DisposalDestination, DomainRecord } from '../../types/domain';

interface EditableDestination { facilityName: string; licenseNumber: string }

@Component({
  selector: 'app-destination-editor',
  standalone: true,
  imports: [CommonModule, FormsModule, MatButtonModule],
  template: `
    <div *ngIf="open" class="modal-backdrop" (click)="cancel.emit()">
      <section class="modal modal--wide" role="dialog" aria-modal="true" aria-labelledby="destination-title" (click)="$event.stopPropagation()">
        <h2 id="destination-title">备案处置去向 · {{ record?.code }}</h2>
        <p class="editor-hint">逐项登记处理厂名称与经营许可证号；同一单位可备案多处。保存后列表中缺失的备案将被标记为撤销。</p>

        <div class="destination-rows">
          <div class="destination-row" *ngFor="let row of rows; let i = index">
            <input [(ngModel)]="row.facilityName" name="facility-{{ i }}" placeholder="处理厂名称，如：合规处置中心 A" aria-label="处理厂名称" />
            <input [(ngModel)]="row.licenseNumber" name="license-{{ i }}" placeholder="经营许可证号" aria-label="经营许可证号" />
            <button type="button" mat-button color="warn" (click)="removeRow(i)" [disabled]="rows.length <= 1">移除</button>
          </div>
        </div>
        <button type="button" class="link-button" (click)="addRow()">＋ 增加一处备案去向</button>

        <div class="revoked-list" *ngIf="revoked.length">
          <small>已撤销备案（保留备查，不再用于联单核验）：</small>
          <span class="revoked-chip" *ngFor="let item of revoked">{{ item.facilityName }} · {{ item.licenseNumber }}</span>
        </div>

        <div class="alert" *ngIf="requestError || error" role="alert">{{ requestError || error }}</div>
        <footer>
          <button mat-button (click)="cancel.emit()">取消</button>
          <button mat-flat-button color="primary" (click)="emitSave()" [disabled]="!valid()">保存备案</button>
        </footer>
      </section>
    </div>
  `
})
export class DestinationEditorComponent implements OnChanges {
  @Input() open = false;
  @Input() record: DomainRecord | null = null;
  @Input() requestError = '';
  @Output() save = new EventEmitter<DisposalDestination[]>();
  @Output() cancel = new EventEmitter<void>();

  rows: EditableDestination[] = [];
  revoked: DisposalDestination[] = [];
  error = '';

  ngOnChanges(): void {
    if (this.open) this.reset();
  }

  reset(): void {
    this.error = '';
    this.rows = (this.record?.destinations || [])
      .filter((item) => item.status !== 'revoked')
      .map((item) => ({ facilityName: item.facilityName, licenseNumber: item.licenseNumber }));
    this.revoked = (this.record?.destinations || []).filter((item) => item.status === 'revoked');
    if (!this.rows.length) this.addRow();
  }

  addRow(): void { this.rows.push({ facilityName: '', licenseNumber: '' }); }
  removeRow(index: number): void { this.rows.splice(index, 1); }

  valid(): boolean {
    return this.rows.length > 0
      && this.rows.every((row) => row.facilityName.trim() && row.licenseNumber.trim())
      && !this.hasDuplicate();
  }

  hasDuplicate(): boolean {
    const keys = this.rows.map((row) => `${row.facilityName.trim()}__${row.licenseNumber.trim().toUpperCase()}`);
    return new Set(keys).size !== keys.length;
  }

  emitSave(): void {
    if (!this.valid()) {
      this.error = '每处备案都需要处理厂名称和经营许可证号，且不能重复。';
      return;
    }
    this.save.emit(this.rows.map((row) => ({
      facilityName: row.facilityName.trim(),
      licenseNumber: row.licenseNumber.trim().toUpperCase()
    })));
  }
}
