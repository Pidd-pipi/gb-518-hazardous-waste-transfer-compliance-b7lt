import { CommonModule } from '@angular/common';
import { Component, EventEmitter, Input, OnChanges, Output } from '@angular/core';
import { FormsModule } from '@angular/forms';
import { MatButtonModule } from '@angular/material/button';
import { updateWasteGenerator } from '../../api/waste-generator';
import type { DomainRecord, RegisteredDestination } from '../../types/domain';

interface DestinationDraft {
  facilityName: string;
  licenseNo: string;
  status: 'active' | 'revoked';
}

// DestinationDialogComponent maintains the optional 备案去向 list of one
// 产废单位. Saving goes through the generator update endpoint so the change
// shares the optimistic-lock version and audit trail of the unit archive.
@Component({
  selector: 'app-destination-dialog',
  standalone: true,
  imports: [CommonModule, FormsModule, MatButtonModule],
  template: `
    <div *ngIf="open" class="modal-backdrop" (click)="cancel.emit()">
      <section class="modal modal--wide" role="dialog" aria-modal="true" aria-labelledby="destination-title" (click)="$event.stopPropagation()">
        <h2 id="destination-title">备案去向 · {{ record?.code }}</h2>
        <p class="muted">
          登记允许接收该单位危险废物的处置厂（处理厂名称 + 经营许可证号），可备案多处。
          联单提交与发运时会与当前备案逐项核对；已提交和运输中的联单不受后续调整影响。
        </p>
        <table class="destination-table">
          <thead><tr><th>处理厂名称</th><th>经营许可证号</th><th>备案状态</th><th></th></tr></thead>
          <tbody>
            <tr *ngFor="let row of rows; let i = index">
              <td><input [(ngModel)]="row.facilityName" placeholder="例如：合规处置中心 A" aria-label="处理厂名称" /></td>
              <td><input [(ngModel)]="row.licenseNo" placeholder="例如：DISP-LIC-A-001" aria-label="经营许可证号" /></td>
              <td>
                <select [(ngModel)]="row.status" aria-label="备案状态">
                  <option value="active">备案有效</option>
                  <option value="revoked">已撤销</option>
                </select>
              </td>
              <td><button type="button" class="table-action" (click)="removeRow(i)">移除</button></td>
            </tr>
            <tr *ngIf="!rows.length"><td colspan="4" class="empty">暂未备案去向，联单去向暂不强制核对</td></tr>
          </tbody>
        </table>
        <button type="button" class="table-action" (click)="addRow()">＋ 添加备案去向</button>
        <div *ngIf="error" class="alert" role="alert">{{ error }}</div>
        <footer>
          <button mat-button (click)="cancel.emit()">取消</button>
          <button mat-flat-button color="primary" [disabled]="saving" (click)="save()">保存备案</button>
        </footer>
      </section>
    </div>
  `
})
export class DestinationDialogComponent implements OnChanges {
  @Input() open = false;
  @Input() record: DomainRecord | null = null;
  @Output() cancel = new EventEmitter<void>();
  @Output() saved = new EventEmitter<void>();
  rows: DestinationDraft[] = [];
  error = '';
  saving = false;

  ngOnChanges(): void {
    if (this.open && this.record) {
      this.rows = (this.record.registeredDestinations ?? []).map((item) => ({
        facilityName: item.facilityName,
        licenseNo: item.licenseNo,
        status: item.status === 'revoked' ? 'revoked' : 'active'
      }));
      this.error = '';
    }
  }

  addRow(): void {
    this.rows.push({ facilityName: '', licenseNo: '', status: 'active' });
  }

  removeRow(index: number): void {
    this.rows.splice(index, 1);
  }

  async save(): Promise<void> {
    if (!this.record) return;
    const destinations: RegisteredDestination[] = this.rows
      .map((row) => ({ facilityName: row.facilityName.trim(), licenseNo: row.licenseNo.trim(), status: row.status }))
      .filter((row) => row.facilityName !== '' || row.licenseNo !== '');
    if (destinations.some((row) => row.facilityName === '' || row.licenseNo === '')) {
      this.error = '每条备案去向都需要填写处理厂名称和经营许可证号。';
      return;
    }
    this.saving = true;
    this.error = '';
    try {
      await updateWasteGenerator(this.record.id, this.buildPayload(destinations));
      this.saved.emit();
    } catch (cause) {
      this.error = cause instanceof Error ? cause.message : String(cause);
    } finally {
      this.saving = false;
    }
  }

  private buildPayload(destinations: RegisteredDestination[]): Partial<DomainRecord> {
    const record = this.record as DomainRecord;
    return {
      expectedVersion: record.version,
      name: record.name,
      permitNumber: record.permitNumber,
      permitExpiresAt: record.permitExpiresAt,
      wasteCategories: record.wasteCategories,
      description: record.description,
      facility: record.facility,
      owner: record.owner,
      category: record.category,
      riskLevel: record.riskLevel,
      metricValue: record.metricValue,
      metricUnit: record.metricUnit,
      effectiveAt: record.effectiveAt,
      evidence: record.evidence,
      relatedCode: record.relatedCode,
      registeredDestinations: destinations
    };
  }
}
